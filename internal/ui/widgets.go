package ui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// ── Card ───────────────────────────────────────────────────────────────────────

// Card returns a visually elevated panel with rounded corners.
// content is inset with generous padding.
func Card(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colorSurface2)
	bg.CornerRadius = 12
	bg.StrokeColor = colorSeparator
	bg.StrokeWidth = 1

	inner := container.NewPadded(content)
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12), inner))
}

// CardWithTitle returns an elevated card with a header strip and body.
func CardWithTitle(title string, content fyne.CanvasObject) fyne.CanvasObject {
	// header bar
	headerBg := canvas.NewRectangle(colorSurface3)
	headerBg.CornerRadius = 0 // will be clipped by outer card
	titleText := canvas.NewText(title, colorFgMuted)
	titleText.TextStyle = fyne.TextStyle{Bold: true}
	titleText.TextSize = 10
	header := container.NewStack(
		headerBg,
		container.New(layout.NewCustomPaddedLayout(7, 7, 12, 12), titleText),
	)

	sep := canvas.NewLine(colorSeparator)
	sep.StrokeWidth = 1

	bodyPadded := container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12), content)

	inner := container.NewVBox(header, sep, bodyPadded)

	bg := canvas.NewRectangle(colorSurface2)
	bg.CornerRadius = 12
	bg.StrokeColor = colorSeparator
	bg.StrokeWidth = 1

	return container.NewStack(bg, inner)
}

// ── StatusBadge ───────────────────────────────────────────────────────────────

// StatusBadge returns a small colored pill label.
func StatusBadge(text string, col color.Color) fyne.CanvasObject {
	bg := canvas.NewRectangle(withAlpha(col, 0x28))
	bg.CornerRadius = 8
	bg.StrokeColor = withAlpha(col, 0x70)
	bg.StrokeWidth = 1

	label := canvas.NewText(text, col)
	label.TextSize = 11
	label.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewStack(
		bg,
		container.New(layout.NewCustomPaddedLayout(3, 3, 8, 8), label),
	)
}

// ── Divider ───────────────────────────────────────────────────────────────────

// Divider returns a thin horizontal separator with optional label.
func Divider() fyne.CanvasObject {
	line := canvas.NewLine(colorSeparator)
	line.StrokeWidth = 1
	return container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), line)
}

// ── Label helpers ──────────────────────────────────────────────────────────────

// HeadingLabel returns a large, bold heading text.
func HeadingLabel(text string) *canvas.Text {
	t := canvas.NewText(text, colorFg)
	t.TextSize = 18
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// SubheadingLabel returns a medium semi-bold label.
func SubheadingLabel(text string) *canvas.Text {
	t := canvas.NewText(text, colorFg)
	t.TextSize = 13
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// MutedLabel returns a small, muted auxiliary text.
func MutedLabel(text string) *canvas.Text {
	t := canvas.NewText(text, colorFgMuted)
	t.TextSize = 11
	return t
}

// ── ProgressTracker ───────────────────────────────────────────────────────────

// ProgressTracker displays a rich progress row: "X / Y parts received" with
// a filled progress bar.  Call Refresh() to update after changing the widget
// state.
type ProgressTracker struct {
	widget.BaseWidget

	received int
	total    int
}

// NewProgressTracker creates a new ProgressTracker.
func NewProgressTracker() *ProgressTracker {
	p := &ProgressTracker{}
	p.ExtendBaseWidget(p)
	return p
}

// Update sets the current progress and refreshes the widget.
func (p *ProgressTracker) Update(received, total int) {
	p.received = received
	p.total = total
	p.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (p *ProgressTracker) CreateRenderer() fyne.WidgetRenderer {
	p.ExtendBaseWidget(p)

	track := canvas.NewRectangle(colorSurface3)
	track.CornerRadius = 4

	fill := canvas.NewRectangle(colorPrimary)
	fill.CornerRadius = 4

	countText := canvas.NewText("", colorFg)
	countText.TextSize = 13
	countText.TextStyle = fyne.TextStyle{Bold: true}

	pctText := canvas.NewText("", colorFgMuted)
	pctText.TextSize = 11

	r := &progressRenderer{
		track:     track,
		fill:      fill,
		countText: countText,
		pctText:   pctText,
		p:         p,
	}
	r.refresh()
	return r
}

type progressRenderer struct {
	track     *canvas.Rectangle
	fill      *canvas.Rectangle
	countText *canvas.Text
	pctText   *canvas.Text
	p         *ProgressTracker
}

func (r *progressRenderer) Layout(size fyne.Size) {
	const barH float32 = 6
	const textH float32 = 16
	const gap float32 = 6

	// count text top
	r.countText.Move(fyne.NewPos(0, 0))
	r.countText.Resize(fyne.NewSize(size.Width*0.6, textH))

	// pct text top-right
	r.pctText.Move(fyne.NewPos(size.Width*0.65, 2))
	r.pctText.Resize(fyne.NewSize(size.Width*0.35, textH))

	// progress bar below
	barY := textH + gap
	r.track.Move(fyne.NewPos(0, barY))
	r.track.Resize(fyne.NewSize(size.Width, barH))

	// fill
	r.fill.Move(fyne.NewPos(0, barY))
	fillW := float32(0)
	if r.p.total > 0 {
		fillW = size.Width * float32(r.p.received) / float32(r.p.total)
	}
	if fillW < 0 {
		fillW = 0
	}
	r.fill.Resize(fyne.NewSize(fillW, barH))
}

func (r *progressRenderer) MinSize() fyne.Size {
	return fyne.NewSize(180, 28)
}

func (r *progressRenderer) Refresh() {
	r.refresh()
	canvas.Refresh(r.p)
}

func (r *progressRenderer) refresh() {
	if r.p.total == 0 {
		r.countText.Text = "Waiting for parts…"
		r.pctText.Text = ""
	} else {
		r.countText.Text = fmt.Sprintf("%d / %d parts", r.p.received, r.p.total)
		pct := float32(r.p.received) * 100 / float32(r.p.total)
		r.pctText.Text = fmt.Sprintf("%.0f%%", pct)
	}
	r.countText.Color = colorFg
	r.pctText.Color = colorFgMuted

	if r.p.total > 0 && r.p.received == r.p.total {
		r.fill.FillColor = colorSuccess
	} else {
		r.fill.FillColor = colorPrimary
	}

	r.countText.Refresh()
	r.pctText.Refresh()
	r.track.Refresh()
	r.fill.Refresh()
}

func (r *progressRenderer) Destroy() {}

func (r *progressRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill, r.countText, r.pctText}
}

// ── QRFrame ───────────────────────────────────────────────────────────────────

// QRFrame wraps a QR image with a dark card background and glowing border.
func QRFrame(img *canvas.Image) fyne.CanvasObject {
	outer := canvas.NewRectangle(colorSurface2)
	outer.CornerRadius = 16
	outer.StrokeColor = colorPrimaryGlow
	outer.StrokeWidth = 2

	corner := canvas.NewRectangle(colorSurface3)
	corner.CornerRadius = 12
	corner.StrokeColor = colorSeparator
	corner.StrokeWidth = 1

	imgPadded := container.New(
		layout.NewCustomPaddedLayout(16, 16, 16, 16),
		img,
	)

	return container.NewStack(outer, corner, imgPadded)
}

// ── PartBadge ──────────────────────────────────────────────────────────────────

// PartBadge returns a styled "Part N of M" display.
func PartBadge(text string) fyne.CanvasObject {
	bg := canvas.NewRectangle(colorPrimaryGlow)
	bg.CornerRadius = 10
	bg.StrokeColor = withAlpha(colorPrimary, 0x50)
	bg.StrokeWidth = 1

	label := canvas.NewText(text, colorPrimary)
	label.TextSize = 13
	label.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewStack(
		bg,
		container.New(layout.NewCustomPaddedLayout(5, 5, 14, 14), label),
	)
}

// ── AppHeader ──────────────────────────────────────────────────────────────────

// AppHeader returns a full-width header strip with app name and role badge.
func AppHeader(appName, role string, roleColor color.Color) fyne.CanvasObject {
	bg := canvas.NewRectangle(colorSurface3)
	bg.StrokeColor = colorSeparator
	bg.StrokeWidth = 0 // no stroke on header

	nameText := canvas.NewText(appName, colorFg)
	nameText.TextSize = 15
	nameText.TextStyle = fyne.TextStyle{Bold: true}

	dot := canvas.NewCircle(roleColor)
	dot.Resize(fyne.NewSize(7, 7))

	roleText := canvas.NewText(role, colorFgMuted)
	roleText.TextSize = 11

	dotContainer := container.NewCenter(dot)

	badge := container.NewHBox(dotContainer, widget.NewLabel(""), roleText)

	left := container.NewHBox(nameText)
	right := badge
	row := container.NewBorder(nil, nil, left, right)

	return container.NewStack(
		bg,
		container.New(layout.NewCustomPaddedLayout(14, 14, 16, 16), row),
	)
}

// ── LabelRow ───────────────────────────────────────────────────────────────────

// LabelRow returns a horizontal label + value row, used in info panels.
func LabelRow(label, value string) fyne.CanvasObject {
	lbl := canvas.NewText(label, colorFgMuted)
	lbl.TextSize = 12

	val := canvas.NewText(value, colorFg)
	val.TextSize = 12
	val.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewBorder(nil, nil, lbl, nil, container.NewHBox(layout.NewSpacer(), val))
}

// ── FieldRow ───────────────────────────────────────────────────────────────────

// FieldRow returns a label + widget in a styled horizontal row.
func FieldRow(label string, w fyne.CanvasObject) fyne.CanvasObject {
	lbl := canvas.NewText(label, colorFgMuted)
	lbl.TextSize = 12

	return container.NewBorder(nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, 0, 8), lbl), nil, w)
}

// ── CoordField ────────────────────────────────────────────────────────────────

// CoordField returns a labeled numeric entry styled as a compact field.
func CoordField(label string, entry *widget.Entry) fyne.CanvasObject {
	lbl := canvas.NewText(label, colorFgMuted)
	lbl.TextSize = 10
	lbl.TextStyle = fyne.TextStyle{Bold: true}

	bg := canvas.NewRectangle(colorInputBg)
	bg.CornerRadius = 6
	bg.StrokeColor = colorInputBorder
	bg.StrokeWidth = 1

	return container.NewVBox(
		lbl,
		container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), entry)),
	)
}

// ── withAlpha ─────────────────────────────────────────────────────────────────

// withAlpha returns a copy of c with the given alpha value (0-255).
func withAlpha(c color.Color, a uint8) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: a,
	}
}
