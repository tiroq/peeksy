// Peeksy Receiver – captures QR codes displayed by the sender, reassembles
// the file, and saves it to disk.
//
// Usage: peeksy-receiver
//
// Workflow:
//  1. Choose an output folder with "Browse…".
//  2. Set the screen region (X, Y, W, H) that contains the sender's QR code.
//  3. Click "Capture & Read QR" to scan one frame.
//     The receiver decodes the QR, stores the chunk, and calls the sender's
//     HTTP API (/api/next) to advance to the next part.
//  4. Repeat (or enable Auto-Capture for hands-free operation).
//  5. When all parts are received the "Save File" button becomes active.
//  6. If parts are missing use "Request Resend" to tell the sender to
//     re-display the missing part.
package main

import (
	"bytes"
	"fmt"
	"image"
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

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
	ui "github.com/tiroq/peeksy/internal/ui"
)

const defaultSenderAddr = "http://127.0.0.1:8765"

// receiverState is the mutable state shared across goroutines.
type receiverState struct {
	mu         sync.Mutex
	received   map[int]chunker.Chunk // index → chunk
	total      int                   // total expected (0 = unknown)
	outputDir  string
	outputName string // original filename hint (from first chunk, not stored in protocol)
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
	x := rx
	y := ry
	w := rw
	h := rh
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

func main() {
	state := newReceiverState()

	a := app.New()
	a.Settings().SetTheme(&ui.ObsidianTheme{})
	w := a.NewWindow("Peeksy — Receiver")
	w.Resize(fyne.NewSize(500, 680))

	// ── widgets ──────────────────────────────────────────────────────────────

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

	// Detail labels (received/missing)
	receivedLabel := canvas.NewText("Received: –", ui.ColorFgMuted)
	receivedLabel.TextSize = 11
	missingLabel := canvas.NewText("Missing: –", ui.ColorFgMuted)
	missingLabel.TextSize = 11

	// Status indicator text
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

	// ── helpers ───────────────────────────────────────────────────────────────

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

		// Tell sender to advance to the next part.
		senderAddr := strings.TrimRight(senderEntry.Text, "/")
		if senderAddr != "" {
			if err := callSenderAPI(senderAddr, "/api/next"); err != nil {
				log.Printf("warn: could not advance sender: %v", err)
			}
		}
		return nil
	}

	// ── auto-capture ──────────────────────────────────────────────────────────

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

	// ── button handlers ───────────────────────────────────────────────────────

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

	// ── browse output folder ──────────────────────────────────────────────────

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

	// ── layout ────────────────────────────────────────────────────────────────

	// Header
	header := ui.AppHeader("peeksy", "receiver", ui.ColorAccent)

	// Output folder card
	outputSaveRow := container.NewBorder(nil, nil, nil, browseBtn, outputRow)
	outputCard := ui.CardWithTitle("OUTPUT FOLDER", outputSaveRow)

	// Connection card
	connCard := ui.CardWithTitle("SENDER CONNECTION",
		ui.FieldRow("Address", senderEntry),
	)

	// Region card – compact 4-column grid
	regionGrid := container.NewGridWithColumns(4,
		ui.CoordField("X", rxEntry),
		ui.CoordField("Y", ryEntry),
		ui.CoordField("W", rwEntry),
		ui.CoordField("H", rhEntry),
	)
	regionCard := ui.CardWithTitle("SCREEN REGION (px)", regionGrid)

	// Capture buttons row
	captureRow := container.NewHBox(captureBtn, autoBtn, stopBtn)

	// Progress card
	detailsCol := container.NewVBox(receivedLabel, missingLabel)
	progressContent := container.NewVBox(
		progressTracker,
		container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), detailsCol),
	)
	progressCard := ui.CardWithTitle("PROGRESS", progressContent)

	// Status row
	statusLine := container.NewBorder(nil, nil, nil, nil, statusRow)

	// Actions row
	actionsRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(resendBtn, saveBtn),
	)

	// Main body
	body := container.NewVBox(
		outputCard,
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), connCard),
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), regionCard),
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0),
			ui.Card(
				container.NewVBox(captureRow, statusLine),
			),
		),
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), progressCard),
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), actionsRow),
	)

	// App background
	bgRect := canvas.NewRectangle(ui.ColorBackground)

	content := container.NewVBox(
		container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), header),
		container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12), body),
	)

	root := container.NewStack(
		bgRect,
		container.NewVScroll(content),
	)

	w.SetContent(root)
	w.ShowAndRun()
}
