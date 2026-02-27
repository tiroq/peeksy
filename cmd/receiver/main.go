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
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	gozxing "github.com/makiuchi-d/gozxing"
	gozxingqr "github.com/makiuchi-d/gozxing/qrcode"
	screenshot "github.com/kbinani/screenshot"

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
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
	// Clamp region to display bounds.
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
	w := a.NewWindow("Peeksy – Receiver")
	w.Resize(fyne.NewSize(520, 580))

	// ── widgets ──────────────────────────────────────────────────────────────

	outputLabel := widget.NewLabel("(none)")

	senderEntry := widget.NewEntry()
	senderEntry.SetText(defaultSenderAddr)
	senderEntry.SetPlaceHolder("http://127.0.0.1:8765")

	rxEntry := widget.NewEntry()
	rxEntry.SetText("0")
	ryEntry := widget.NewEntry()
	ryEntry.SetText("0")
	rwEntry := widget.NewEntry()
	rwEntry.SetText("400")
	rhEntry := widget.NewEntry()
	rhEntry.SetText("400")

	progressLabel := widget.NewLabel("No parts received yet")
	receivedLabel := widget.NewLabel("Received: –")
	missingLabel := widget.NewLabel("Missing: –")

	captureBtn := widget.NewButtonWithIcon("Capture & Read QR", theme.MediaPhotoIcon(), nil)
	autoBtn := widget.NewButtonWithIcon("Start Auto-Capture", theme.MediaPlayIcon(), nil)
	stopBtn := widget.NewButtonWithIcon("Stop Auto-Capture", theme.MediaStopIcon(), nil)
	resendBtn := widget.NewButtonWithIcon("Request Resend", theme.WarningIcon(), nil)
	saveBtn := widget.NewButtonWithIcon("Save File", theme.DocumentSaveIcon(), nil)

	stopBtn.Disable()
	saveBtn.Disable()

	// ── helpers ───────────────────────────────────────────────────────────────

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
		if total == 0 {
			progressLabel.SetText("No parts received yet")
			receivedLabel.SetText("Received: –")
			missingLabel.SetText("Missing: –")
			saveBtn.Disable()
			return
		}

		progressLabel.SetText(fmt.Sprintf("Progress: %d / %d parts", count, total))

		sort.Slice(cks, func(i, j int) bool { return cks[i].Index < cks[j].Index })
		idxStr := make([]string, len(cks))
		for i, c := range cks {
			idxStr[i] = strconv.Itoa(c.Index)
		}
		receivedLabel.SetText("Received: " + strings.Join(idxStr, ", "))

		miss := chunker.MissingIndices(cks, total)
		if len(miss) == 0 {
			missingLabel.SetText("Missing: none ✓")
			saveBtn.Enable()
		} else {
			missStr := make([]string, len(miss))
			for i, m := range miss {
				missStr[i] = strconv.Itoa(m)
			}
			missingLabel.SetText("Missing: " + strings.Join(missStr, ", "))
			saveBtn.Disable()
		}
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
	}

	// ── button handlers ───────────────────────────────────────────────────────

	captureBtn.OnTapped = func() {
		if err := captureOnce(); err != nil {
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
		label := widget.NewLabel("Missing parts: " + strings.Join(missStr, ", "))

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

	browseBtn := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			state.outputDir = uri.Path()
			outputLabel.SetText(uri.Path())
		}, w)
	})

	// ── layout ────────────────────────────────────────────────────────────────

	outputRow := container.NewBorder(nil, nil, widget.NewLabel("Save to:"), browseBtn, outputLabel)
	senderRow := container.NewBorder(nil, nil, widget.NewLabel("Sender:"), nil, senderEntry)

	regionRow := container.NewGridWithColumns(4,
		container.NewVBox(widget.NewLabel("X"), rxEntry),
		container.NewVBox(widget.NewLabel("Y"), ryEntry),
		container.NewVBox(widget.NewLabel("W"), rwEntry),
		container.NewVBox(widget.NewLabel("H"), rhEntry),
	)

	captureRow := container.NewHBox(captureBtn, autoBtn, stopBtn)

	content := container.NewVBox(
		widget.NewSeparator(),
		outputRow,
		senderRow,
		widget.NewSeparator(),
		widget.NewLabel("Screen region to capture:"),
		regionRow,
		widget.NewSeparator(),
		captureRow,
		widget.NewSeparator(),
		progressLabel,
		receivedLabel,
		missingLabel,
		widget.NewSeparator(),
		container.NewHBox(resendBtn, saveBtn),
	)

	w.SetContent(container.NewPadded(content))
	w.ShowAndRun()
}
