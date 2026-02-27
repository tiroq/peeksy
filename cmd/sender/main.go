// Peeksy Sender – splits a file into QR-code chunks that a receiver can scan.
//
// Usage: peeksy-sender
//
// The sender exposes a small HTTP API on :8765 so the receiver can request
// specific parts be re-displayed:
//
//	GET  /api/info           → {"total":N,"current":K}
//	POST /api/next           → advance to next part
//	POST /api/prev           → go to previous part
//	POST /api/goto/{n}       → jump to part n
//	GET  /api/qr.png         → current QR as PNG (for testing)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrlib "github.com/skip2/go-qrcode"

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
)

const (
	senderPort  = 8765
	qrPixelSize = 400
)

// senderState holds the mutable state shared between the GUI and HTTP server.
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

// decodeQRPNG turns raw PNG bytes into an image.Image for fyne.
func decodeQRPNG(raw []byte) (image.Image, error) {
	return png.Decode(bytes.NewReader(raw))
}

func main() {
	state := &senderState{}

	a := app.New()
	w := a.NewWindow("Peeksy – Sender")
	w.Resize(fyne.NewSize(500, 600))

	// ── widgets ──────────────────────────────────────────────────────────────

	fileLabel := widget.NewLabel("No file selected")

	// placeholder grey QR image
	placeholder := image.NewGray(image.Rect(0, 0, qrPixelSize, qrPixelSize))
	qrImg := canvas.NewImageFromImage(placeholder)
	qrImg.FillMode = canvas.ImageFillContain
	qrImg.SetMinSize(fyne.NewSize(float32(qrPixelSize), float32(qrPixelSize)))

	partLabel := widget.NewLabel("")
	partLabel.Alignment = fyne.TextAlignCenter
	partLabel.TextStyle = fyne.TextStyle{Bold: true}

	prevBtn := widget.NewButtonWithIcon("Prev", theme.NavigateBackIcon(), nil)
	nextBtn := widget.NewButtonWithIcon("Next", theme.NavigateNextIcon(), nil)
	resendBtn := widget.NewButtonWithIcon("Resend Part…", theme.WarningIcon(), nil)

	prevBtn.Disable()
	nextBtn.Disable()
	resendBtn.Disable()

	// ── helpers ───────────────────────────────────────────────────────────────

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
		partLabel.SetText(fmt.Sprintf("Part %d of %d", c.Index, total))

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

	// ── button handlers ───────────────────────────────────────────────────────

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
		d := dialog.NewForm("Resend Part", "Show", "Cancel",
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

	// ── file selection ────────────────────────────────────────────────────────

	selectBtn := widget.NewButtonWithIcon("Select File…", theme.FolderOpenIcon(), func() {
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
			fileLabel.SetText(filepath.Base(path))
			refreshQR()
			nextBtn.Enable()
			resendBtn.Enable()
		}, w)
		fd.Show()
	})

	// ── HTTP server ───────────────────────────────────────────────────────────

	go startHTTPServer(state)

	// ── layout ────────────────────────────────────────────────────────────────

	topBar := container.NewHBox(selectBtn, fileLabel)
	navBar := container.NewHBox(prevBtn, nextBtn, resendBtn)
	content := container.NewVBox(
		topBar,
		container.NewCenter(qrImg),
		container.NewCenter(partLabel),
		container.NewCenter(navBar),
	)

	w.SetContent(content)
	w.ShowAndRun()
}

// ── HTTP server ───────────────────────────────────────────────────────────────

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
