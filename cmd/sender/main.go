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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrlib "github.com/skip2/go-qrcode"

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
	ui "github.com/tiroq/peeksy/internal/ui"
)

const (
	senderPort  = 8765
	qrPixelSize = 380
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
	a.Settings().SetTheme(&ui.ObsidianTheme{})
	w := a.NewWindow("Peeksy — Sender")
	w.Resize(fyne.NewSize(480, 680))

	// ── placeholders ─────────────────────────────────────────────────────────

	placeholder := image.NewGray(image.Rect(0, 0, qrPixelSize, qrPixelSize))
	qrImg := canvas.NewImageFromImage(placeholder)
	qrImg.FillMode = canvas.ImageFillContain
	qrImg.SetMinSize(fyne.NewSize(300, 300))

	// ── part indicator ───────────────────────────────────────────────────────

	partText := canvas.NewText("Select a file to begin", ui.ColorFgMuted)
	partText.TextSize = 13
	partText.TextStyle = fyne.TextStyle{Bold: true}
	partTextContainer := container.NewCenter(partText)

	// ── file info row ────────────────────────────────────────────────────────

	fileNameText := canvas.NewText("No file selected", ui.ColorFgMuted)
	fileNameText.TextSize = 12
	fileNameText.TextStyle = fyne.TextStyle{}

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

	// ── navigation buttons ───────────────────────────────────────────────────

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

	// ── HTTP status badge ────────────────────────────────────────────────────

	apiDot := canvas.NewCircle(ui.ColorSuccess)
	apiDot.Resize(fyne.NewSize(6, 6))
	apiText := canvas.NewText(fmt.Sprintf("API :  %d", senderPort), ui.ColorFgMuted)
	apiText.TextSize = 11
	apiStatusRow := container.NewHBox(
		container.NewCenter(apiDot),
		container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0), apiText),
	)

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

	// ── file selection ────────────────────────────────────────────────────────

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

	// ── HTTP server ───────────────────────────────────────────────────────────

	go startHTTPServer(state)

	// ── layout ────────────────────────────────────────────────────────────────

	// Header
	header := ui.AppHeader("peeksy", "sender", ui.ColorPrimary)

	// QR display
	qrFrame := ui.QRFrame(qrImg)
	qrSection := container.NewCenter(qrFrame)

	// Part badge + text row (under QR)
	qrInfoSection := container.NewCenter(partTextContainer)

	// Navigation row
	prevContainer := container.NewCenter(prevBtn)

	navRow := container.NewBorder(nil, nil, prevContainer, nil,
		container.NewHBox(layout.NewSpacer(), resendBtn, nextBtn),
	)

	// QR card (frame + info + nav)
	qrCard := ui.Card(
		container.NewVBox(
			qrSection,
			container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), qrInfoSection),
			ui.Divider(),
			navRow,
		),
	)

	// File info card
	fileCard := ui.CardWithTitle("FILE", fileInfoRow)

	// Bottom toolbar
	selectRow := container.NewHBox(layout.NewSpacer(), selectBtn)

	// API status
	apiRow := container.NewHBox(layout.NewSpacer(), apiStatusRow)

	// Main scroll content
	content := container.NewVBox(
		container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), header),
		container.New(layout.NewCustomPaddedLayout(12, 0, 12, 12),
			container.NewVBox(
				fileCard,
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), qrCard),
				container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), selectRow),
				container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), apiRow),
			),
		),
	)

	// App background
	bgRect := canvas.NewRectangle(ui.ColorBackground)

	root := container.NewStack(
		bgRect,
		container.NewVBox(content),
	)

	w.SetContent(root)
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

