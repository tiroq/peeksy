// Peeksy – unified binary combining Sender and Receiver in a single window.
//
// The window contains two tabs:
//
//	"Send"    – splits a file into QR-code chunks and exposes an HTTP API
//	"Receive" – captures QR codes, reassembles the file, and saves it to disk
//
// HTTP API (Sender, port 8765):
//
//	GET  /api/info           → {"total":N,"current":K}
//	POST /api/next           → advance to next part
//	POST /api/prev           → go to previous part
//	POST /api/goto/{n}       → jump to part n
//	GET  /api/qr.png         → current QR as PNG
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	screenshot "github.com/kbinani/screenshot"
	gozxing "github.com/makiuchi-d/gozxing"
	gozxingqr "github.com/makiuchi-d/gozxing/qrcode"
	qrlib "github.com/skip2/go-qrcode"

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
	ui "github.com/tiroq/peeksy/internal/ui"
)

// ── constants ──────────────────────────────────────────────────────────────────

const (
	senderPort        = 8765
	qrPixelSize       = 380
	defaultSenderAddr = "http://127.0.0.1:8765"
)

// ══════════════════════════════════════════════════════════════════════════════
// Sender state & logic
// ══════════════════════════════════════════════════════════════════════════════

// senderState holds the mutable state shared between the Sender GUI and HTTP server.
type senderState struct {
	mu      sync.RWMutex
	chunks  []chunker.Chunk
	current int // 0-based index into chunks
}

func (s *senderState) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.chunks)
}

func (s *senderState) currentChunk() (chunker.Chunk, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.chunks) == 0 {
		return chunker.Chunk{}, false
	}
	return s.chunks[s.current], true
}

func (s *senderState) goTo(idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.chunks) {
		return false
	}
	s.current = idx
	return true
}

func (s *senderState) next() bool { return s.goTo(s.getCurrent() + 1) }
func (s *senderState) prev() bool { return s.goTo(s.getCurrent() - 1) }

func (s *senderState) getCurrent() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *senderState) load(path string) error {
	cks, err := chunker.SplitFile(path, 0)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = cks
	s.current = 0
	return nil
}

// generateQRPNG encodes the current chunk as a QR-code PNG.
func generateQRPNG(c chunker.Chunk) ([]byte, error) {
	payload, err := protocol.Encode(c)
	if err != nil {
		return nil, err
	}
	return qrlib.Encode(payload, qrlib.Medium, qrPixelSize)
}

// decodeQRPNG turns raw PNG bytes into an image.Image for Fyne.
func decodeQRPNG(raw []byte) (image.Image, error) {
	return png.Decode(bytes.NewReader(raw))
}

// ══════════════════════════════════════════════════════════════════════════════
// Receiver state & logic
// ══════════════════════════════════════════════════════════════════════════════

// receiverState is the mutable state shared across goroutines.
type receiverState struct {
	mu         sync.Mutex
	received   map[int]chunker.Chunk // index → chunk
	total      int                   // total expected (0 = unknown)
	outputDir  string
	outputName string // original filename hint
}

func newReceiverState() *receiverState {
	return &receiverState{received: make(map[int]chunker.Chunk)}
}

func (s *receiverState) addChunk(c chunker.Chunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received[c.Index] = c
	if c.Total > 0 {
		s.total = c.Total
	}
}

func (s *receiverState) snapshot() ([]chunker.Chunk, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]chunker.Chunk, 0, len(s.received))
	for _, c := range s.received {
		out = append(out, c)
	}
	return out, s.total
}

func (s *receiverState) missing() []int {
	cks, total := s.snapshot()
	return chunker.MissingIndices(cks, total)
}

func (s *receiverState) complete() bool {
	cks, total := s.snapshot()
	return total > 0 && len(cks) == total
}

// captureRegion takes a screenshot of the rectangle (rx,ry)-(rx+rw, ry+rh).
func captureRegion(rx, ry, rw, rh int) (image.Image, error) {
	bounds := screenshot.GetDisplayBounds(0)
	x, y, w, h := rx, ry, rw, rh
	if x < bounds.Min.X {
		x = bounds.Min.X
	}
	if y < bounds.Min.Y {
		y = bounds.Min.Y
	}
	if x+w > bounds.Max.X {
		w = bounds.Max.X - x
	}
	if y+h > bounds.Max.Y {
		h = bounds.Max.Y - y
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("capture region is outside display bounds")
	}
	return screenshot.CaptureRect(image.Rect(x, y, x+w, y+h))
}

// decodeQRFromImage reads a QR code from an image and returns the text.
func decodeQRFromImage(img image.Image) (string, error) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("creating bitmap: %w", err)
	}
	reader := gozxingqr.NewQRCodeReader()
	result, err := reader.Decode(bmp, nil)
	if err != nil {
		return "", fmt.Errorf("reading QR code: %w", err)
	}
	return result.GetText(), nil
}

// callSenderAPI sends a POST request to the sender's HTTP API.
func callSenderAPI(addr, path string) error {
	resp, err := http.Post(addr+path, "application/json", nil)
	if err != nil {
		return fmt.Errorf("calling sender %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sender returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// ══════════════════════════════════════════════════════════════════════════════
// HTTP server (Sender)
// ══════════════════════════════════════════════════════════════════════════════

func startHTTPServer(state *senderState) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		total := state.count()
		cur := 0
		if c, ok := state.currentChunk(); ok {
			cur = c.Index
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"total": total, "current": cur})
	})

	mux.HandleFunc("/api/next", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.next()
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/prev", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.prev()
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/goto/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.TrimPrefix(r.URL.Path, "/api/goto/")
		n, err := strconv.Atoi(parts)
		if err != nil || n < 1 {
			http.Error(w, "invalid part number", http.StatusBadRequest)
			return
		}
		if !state.goTo(n - 1) {
			http.Error(w, "part out of range", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/qr.png", func(w http.ResponseWriter, r *http.Request) {
		c, ok := state.currentChunk()
		if !ok {
			http.Error(w, "no file loaded", http.StatusNotFound)
			return
		}
		raw, err := generateQRPNG(c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	})

	addr := fmt.Sprintf(":%d", senderPort)
	log.Printf("sender HTTP API listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Sender tab
// ══════════════════════════════════════════════════════════════════════════════

func buildSenderTab(w fyne.Window) fyne.CanvasObject {
	state := &senderState{}

	// ── placeholders ───────────────────────────────────────────────────────────
	placeholder := image.NewGray(image.Rect(0, 0, qrPixelSize, qrPixelSize))
	qrImg := canvas.NewImageFromImage(placeholder)
	qrImg.FillMode = canvas.ImageFillContain
	qrImg.SetMinSize(fyne.NewSize(300, 300))

	// ── part indicator ─────────────────────────────────────────────────────────
	partText := canvas.NewText("Select a file to begin", ui.ColorFgMuted)
	partText.TextSize = 13
	partText.TextStyle = fyne.TextStyle{Bold: true}
	partTextContainer := container.NewCenter(partText)

	// ── file info row ──────────────────────────────────────────────────────────
	fileNameText := canvas.NewText("No file selected", ui.ColorFgMuted)
	fileNameText.TextSize = 12

	fileInfoBg := canvas.NewRectangle(ui.ColorSurface3)
	fileInfoBg.CornerRadius = 8
	fileInfoBg.StrokeColor = ui.ColorSeparator
	fileInfoBg.StrokeWidth = 1

	folderIcon := widget.NewIcon(theme.FolderIcon())
	folderIcon.Resize(fyne.NewSize(16, 16))

	fileInfoRow := container.NewStack(
		fileInfoBg,
		container.New(layout.NewCustomPaddedLayout(8, 8, 10, 10),
			container.NewBorder(nil, nil, folderIcon, nil,
				container.New(layout.NewCustomPaddedLayout(0, 0, 6, 0), fileNameText),
			),
		),
	)

	// ── navigation buttons ─────────────────────────────────────────────────────
	prevBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), nil)
	prevBtn.Importance = widget.LowImportance
	prevBtn.Disable()

	nextBtn := widget.NewButtonWithIcon("Next", theme.NavigateNextIcon(), nil)
	nextBtn.Importance = widget.HighImportance
	nextBtn.Disable()

	resendBtn := widget.NewButtonWithIcon("Resend Part…", theme.WarningIcon(), nil)
	resendBtn.Importance = widget.MediumImportance
	resendBtn.Disable()

	selectBtn := widget.NewButtonWithIcon("Open File", theme.FolderOpenIcon(), nil)
	selectBtn.Importance = widget.HighImportance

	// ── HTTP status badge ──────────────────────────────────────────────────────
	apiDot := canvas.NewCircle(ui.ColorSuccess)
	apiDot.Resize(fyne.NewSize(6, 6))
	apiText := canvas.NewText(fmt.Sprintf("API :  %d", senderPort), ui.ColorFgMuted)
	apiText.TextSize = 11
	apiStatusRow := container.NewHBox(
		container.NewCenter(apiDot),
		container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0), apiText),
	)

	// ── helpers ────────────────────────────────────────────────────────────────
	refreshQR := func() {
		c, ok := state.currentChunk()
		if !ok {
			return
		}
		raw, err := generateQRPNG(c)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		img, err := decodeQRPNG(raw)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		qrImg.Image = img
		canvas.Refresh(qrImg)

		total := state.count()
		partText.Text = fmt.Sprintf("Part %d of %d", c.Index, total)
		partText.Color = ui.ColorPrimary
		canvas.Refresh(partText)

		cur := state.getCurrent()
		if cur > 0 {
			prevBtn.Enable()
		} else {
			prevBtn.Disable()
		}
		if cur < total-1 {
			nextBtn.Enable()
		} else {
			nextBtn.Disable()
		}
	}

	// ── button handlers ────────────────────────────────────────────────────────
	prevBtn.OnTapped = func() {
		state.prev()
		refreshQR()
	}
	nextBtn.OnTapped = func() {
		state.next()
		refreshQR()
	}
	resendBtn.OnTapped = func() {
		total := state.count()
		if total == 0 {
			return
		}
		entry := widget.NewEntry()
		entry.SetPlaceHolder(fmt.Sprintf("1 – %d", total))
		d := dialog.NewForm("Jump to Part", "Show", "Cancel",
			[]*widget.FormItem{
				widget.NewFormItem("Part number", entry),
			},
			func(ok bool) {
				if !ok || entry.Text == "" {
					return
				}
				n, err := strconv.Atoi(strings.TrimSpace(entry.Text))
				if err != nil || n < 1 || n > total {
					dialog.ShowError(fmt.Errorf("enter a number between 1 and %d", total), w)
					return
				}
				state.goTo(n - 1)
				refreshQR()
			}, w)
		d.Show()
	}

	selectBtn.OnTapped = func() {
		fd := dialog.NewFileOpen(func(f fyne.URIReadCloser, err error) {
			if err != nil || f == nil {
				return
			}
			defer f.Close()
			path := f.URI().Path()
			if err := state.load(path); err != nil {
				dialog.ShowError(err, w)
				return
			}
			base := filepath.Base(path)
			fileNameText.Text = base
			fileNameText.Color = ui.ColorForeground
			canvas.Refresh(fileNameText)
			refreshQR()
			nextBtn.Enable()
			resendBtn.Enable()
		}, w)
		fd.Show()
	}

	// ── HTTP server (once per app lifetime) ────────────────────────────────────
	go startHTTPServer(state)

	// ── layout ─────────────────────────────────────────────────────────────────
	qrFrame := ui.QRFrame(qrImg)
	qrSection := container.NewCenter(qrFrame)
	qrInfoSection := container.NewCenter(partTextContainer)

	prevContainer := container.NewCenter(prevBtn)
	navRow := container.NewBorder(nil, nil, prevContainer, nil,
		container.NewHBox(layout.NewSpacer(), resendBtn, nextBtn),
	)

	qrCard := ui.Card(
		container.NewVBox(
			qrSection,
			container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), qrInfoSection),
			ui.Divider(),
			navRow,
		),
	)

	fileCard := ui.CardWithTitle("FILE", fileInfoRow)
	selectRow := container.NewHBox(layout.NewSpacer(), selectBtn)
	apiRow := container.NewHBox(layout.NewSpacer(), apiStatusRow)

	return container.NewVScroll(
		container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12),
			container.NewVBox(
				fileCard,
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), qrCard),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), selectRow),
				container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), apiRow),
			),
		),
	)
}

// ══════════════════════════════════════════════════════════════════════════════
// Receiver tab
// ══════════════════════════════════════════════════════════════════════════════

func buildReceiverTab(w fyne.Window) fyne.CanvasObject {
	state := newReceiverState()

	// ── widgets ────────────────────────────────────────────────────────────────

	// Output folder
	outputText := canvas.NewText("No folder selected", ui.ColorFgMuted)
	outputText.TextSize = 12

	outputBg := canvas.NewRectangle(ui.ColorSurface3)
	outputBg.CornerRadius = 8
	outputBg.StrokeColor = ui.ColorSeparator
	outputBg.StrokeWidth = 1

	folderIcon := widget.NewIcon(theme.FolderIcon())
	outputRow := container.NewStack(
		outputBg,
		container.New(layout.NewCustomPaddedLayout(8, 8, 10, 10),
			container.NewBorder(nil, nil, folderIcon, nil,
				container.New(layout.NewCustomPaddedLayout(0, 0, 6, 0), outputText),
			),
		),
	)

	browseBtn := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), nil)
	browseBtn.Importance = widget.MediumImportance

	// Sender address
	senderEntry := widget.NewEntry()
	senderEntry.SetText(defaultSenderAddr)
	senderEntry.SetPlaceHolder("http://127.0.0.1:8765")

	// Region entries
	rxEntry := widget.NewEntry()
	rxEntry.SetText("0")
	ryEntry := widget.NewEntry()
	ryEntry.SetText("0")
	rwEntry := widget.NewEntry()
	rwEntry.SetText("400")
	rhEntry := widget.NewEntry()
	rhEntry.SetText("400")

	// Progress tracker
	progressTracker := ui.NewProgressTracker()

	// Detail labels (received / missing)
	receivedLabel := canvas.NewText("Received: –", ui.ColorFgMuted)
	receivedLabel.TextSize = 11
	missingLabel := canvas.NewText("Missing: –", ui.ColorFgMuted)
	missingLabel.TextSize = 11

	// Status indicator
	statusText := canvas.NewText("Ready", ui.ColorFgMuted)
	statusText.TextSize = 11
	statusBg := canvas.NewRectangle(ui.ColorSurface3)
	statusBg.CornerRadius = 6
	statusRow := container.NewStack(
		statusBg,
		container.New(layout.NewCustomPaddedLayout(5, 5, 8, 8), statusText),
	)

	// Action buttons
	captureBtn := widget.NewButtonWithIcon("Capture QR", theme.MediaPhotoIcon(), nil)
	captureBtn.Importance = widget.HighImportance

	autoBtn := widget.NewButtonWithIcon("Auto-Capture", theme.MediaPlayIcon(), nil)
	autoBtn.Importance = widget.MediumImportance

	stopBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), nil)
	stopBtn.Importance = widget.LowImportance
	stopBtn.Disable()

	resendBtn := widget.NewButtonWithIcon("Request Resend", theme.WarningIcon(), nil)
	resendBtn.Importance = widget.MediumImportance

	saveBtn := widget.NewButtonWithIcon("Save File", theme.DocumentSaveIcon(), nil)
	saveBtn.Importance = widget.HighImportance
	saveBtn.Disable()

	// ── helpers ────────────────────────────────────────────────────────────────
	setStatus := func(msg string, col fyne.ThemeColorName) {
		statusText.Text = msg
		statusText.Color = theme.Color(col)
		canvas.Refresh(statusText)
		statusBg.Refresh()
	}

	parseRegion := func() (int, int, int, int, error) {
		rx, err := strconv.Atoi(strings.TrimSpace(rxEntry.Text))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("X: %w", err)
		}
		ry, err := strconv.Atoi(strings.TrimSpace(ryEntry.Text))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("Y: %w", err)
		}
		rw, err := strconv.Atoi(strings.TrimSpace(rwEntry.Text))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("W: %w", err)
		}
		rh, err := strconv.Atoi(strings.TrimSpace(rhEntry.Text))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("H: %w", err)
		}
		if rw <= 0 || rh <= 0 {
			return 0, 0, 0, 0, fmt.Errorf("width and height must be > 0")
		}
		return rx, ry, rw, rh, nil
	}

	refreshProgress := func() {
		cks, total := state.snapshot()
		count := len(cks)
		progressTracker.Update(count, total)

		if total == 0 {
			receivedLabel.Text = "Received: –"
			receivedLabel.Color = ui.ColorFgMuted
			missingLabel.Text = "Missing: –"
			missingLabel.Color = ui.ColorFgMuted
			saveBtn.Disable()
			canvas.Refresh(receivedLabel)
			canvas.Refresh(missingLabel)
			return
		}

		sort.Slice(cks, func(i, j int) bool { return cks[i].Index < cks[j].Index })
		idxStr := make([]string, len(cks))
		for i, c := range cks {
			idxStr[i] = strconv.Itoa(c.Index)
		}
		receivedLabel.Text = "Received: " + strings.Join(idxStr, ", ")
		receivedLabel.Color = ui.ColorForeground

		miss := chunker.MissingIndices(cks, total)
		if len(miss) == 0 {
			missingLabel.Text = "Missing: none ✓"
			missingLabel.Color = ui.ColorSuccess
			saveBtn.Enable()
			setStatus("Complete — all parts received", theme.ColorNameSuccess)
		} else {
			missStr := make([]string, len(miss))
			for i, m := range miss {
				missStr[i] = strconv.Itoa(m)
			}
			missingLabel.Text = "Missing: " + strings.Join(missStr, ", ")
			missingLabel.Color = ui.ColorWarning
			saveBtn.Disable()
		}
		canvas.Refresh(receivedLabel)
		canvas.Refresh(missingLabel)
	}

	captureOnce := func() error {
		rx, ry, rw, rh, err := parseRegion()
		if err != nil {
			return fmt.Errorf("region: %w", err)
		}
		img, err := captureRegion(rx, ry, rw, rh)
		if err != nil {
			return fmt.Errorf("capture: %w", err)
		}
		text, err := decodeQRFromImage(img)
		if err != nil {
			return fmt.Errorf("decode QR: %w", err)
		}
		c, err := protocol.Decode(text)
		if err != nil {
			return fmt.Errorf("parse QR data: %w", err)
		}
		state.addChunk(c)
		refreshProgress()
		setStatus(fmt.Sprintf("Captured part %d", c.Index), theme.ColorNamePrimary)

		senderAddr := strings.TrimRight(senderEntry.Text, "/")
		if senderAddr != "" {
			if err := callSenderAPI(senderAddr, "/api/next"); err != nil {
				log.Printf("warn: could not advance sender: %v", err)
			}
		}
		return nil
	}

	// ── auto-capture ───────────────────────────────────────────────────────────
	var stopCh chan struct{}

	autoBtn.OnTapped = func() {
		if stopCh != nil {
			return
		}
		stopCh = make(chan struct{})
		captureBtn.Disable()
		autoBtn.Disable()
		stopBtn.Enable()
		setStatus("Auto-capture running…", theme.ColorNamePrimary)

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopCh:
					return
				case <-ticker.C:
					if err := captureOnce(); err != nil {
						log.Printf("auto-capture: %v", err)
					}
					if state.complete() {
						fyne.Do(func() {
							stopBtn.OnTapped()
						})
						return
					}
				}
			}
		}()
	}

	stopBtn.OnTapped = func() {
		if stopCh != nil {
			close(stopCh)
			stopCh = nil
		}
		captureBtn.Enable()
		autoBtn.Enable()
		stopBtn.Disable()
		setStatus("Auto-capture stopped", theme.ColorNameForeground)
	}

	// ── button handlers ────────────────────────────────────────────────────────
	captureBtn.OnTapped = func() {
		if err := captureOnce(); err != nil {
			setStatus("Error: "+err.Error(), theme.ColorNameError)
			dialog.ShowError(err, w)
		}
	}

	resendBtn.OnTapped = func() {
		miss := state.missing()
		if len(miss) == 0 {
			dialog.ShowInformation("Nothing missing", "All parts have been received.", w)
			return
		}
		entry := widget.NewEntry()
		entry.SetText(strconv.Itoa(miss[0]))
		entry.SetPlaceHolder("Part number")

		missStr := make([]string, len(miss))
		for i, m := range miss {
			missStr[i] = strconv.Itoa(m)
		}
		label := widget.NewLabel("Missing: " + strings.Join(missStr, ", "))

		d := dialog.NewForm("Request Resend", "Request", "Cancel",
			[]*widget.FormItem{
				widget.NewFormItem("", label),
				widget.NewFormItem("Part to request", entry),
			},
			func(ok bool) {
				if !ok || entry.Text == "" {
					return
				}
				n, err := strconv.Atoi(strings.TrimSpace(entry.Text))
				if err != nil {
					dialog.ShowError(fmt.Errorf("invalid part number"), w)
					return
				}
				senderAddr := strings.TrimRight(senderEntry.Text, "/")
				if err := callSenderAPI(senderAddr, fmt.Sprintf("/api/goto/%d", n)); err != nil {
					dialog.ShowError(err, w)
				}
			}, w)
		d.Show()
	}

	saveBtn.OnTapped = func() {
		if state.outputDir == "" {
			dialog.ShowError(fmt.Errorf("please choose an output folder first"), w)
			return
		}
		cks, _ := state.snapshot()
		raw, err := chunker.Assemble(cks)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		name := state.outputName
		if name == "" {
			name = "received_file.bin"
		}
		outPath := filepath.Join(state.outputDir, name)
		if err := os.WriteFile(outPath, raw, 0600); err != nil {
			dialog.ShowError(err, w)
			return
		}
		dialog.ShowInformation("Saved", "File saved to:\n"+outPath, w)
	}

	browseBtn.OnTapped = func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			state.outputDir = uri.Path()
			outputText.Text = uri.Path()
			outputText.Color = ui.ColorForeground
			canvas.Refresh(outputText)
		}, w)
	}

	// ── layout ─────────────────────────────────────────────────────────────────
	outputSaveRow := container.NewBorder(nil, nil, nil, browseBtn, outputRow)
	outputCard := ui.CardWithTitle("OUTPUT FOLDER", outputSaveRow)

	connCard := ui.CardWithTitle("SENDER CONNECTION",
		ui.FieldRow("Address", senderEntry),
	)

	regionGrid := container.NewGridWithColumns(4,
		ui.CoordField("X", rxEntry),
		ui.CoordField("Y", ryEntry),
		ui.CoordField("W", rwEntry),
		ui.CoordField("H", rhEntry),
	)
	regionCard := ui.CardWithTitle("SCREEN REGION (px)", regionGrid)

	captureRow := container.NewHBox(captureBtn, autoBtn, stopBtn)
	statusLine := container.NewBorder(nil, nil, nil, nil, statusRow)

	detailsCol := container.NewVBox(receivedLabel, missingLabel)
	progressContent := container.NewVBox(
		progressTracker,
		container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), detailsCol),
	)
	progressCard := ui.CardWithTitle("PROGRESS", progressContent)
	actionsRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(resendBtn, saveBtn),
	)

	return container.NewVScroll(
		container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12),
			container.NewVBox(
				outputCard,
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), connCard),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), regionCard),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0),
					ui.Card(container.NewVBox(captureRow, statusLine)),
				),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), progressCard),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), actionsRow),
			),
		),
	)
}

// ══════════════════════════════════════════════════════════════════════════════
// main
// ══════════════════════════════════════════════════════════════════════════════

func main() {
	a := app.New()
	a.Settings().SetTheme(&ui.ObsidianTheme{})

	w := a.NewWindow("Peeksy")
	w.Resize(fyne.NewSize(500, 720))

	header := ui.AppHeader("peeksy", "v1", ui.ColorPrimary)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Send", theme.UploadIcon(), buildSenderTab(w)),
		container.NewTabItemWithIcon("Receive", theme.DownloadIcon(), buildReceiverTab(w)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	bgRect := canvas.NewRectangle(ui.ColorBackground)
	root := container.NewStack(
		bgRect,
		container.NewVBox(
			container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), header),
			tabs,
		),
	)

	w.SetContent(root)
	w.ShowAndRun()
}
