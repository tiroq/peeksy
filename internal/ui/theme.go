// Package ui provides the premium Peeksy visual theme and custom widgets.
//
// Aesthetic: "Obsidian" — deep charcoal background, electric indigo primary,
// soft slate surfaces, crisp white typography.
package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// ── Color palette ──────────────────────────────────────────────────────────────

var (
	// Backgrounds
	colorBg        = color.NRGBA{R: 0x0e, G: 0x0f, B: 0x14, A: 0xff} // near-black
	colorSurface   = color.NRGBA{R: 0x16, G: 0x18, B: 0x22, A: 0xff} // deep navy-charcoal
	colorSurface2  = color.NRGBA{R: 0x1e, G: 0x21, B: 0x2e, A: 0xff} // elevated surface
	colorSurface3  = color.NRGBA{R: 0x26, G: 0x2a, B: 0x3a, A: 0xff} // card / header
	colorSeparator = color.NRGBA{R: 0x2a, G: 0x2d, B: 0x3d, A: 0xff}

	// Primary – electric indigo
	colorPrimary     = color.NRGBA{R: 0x63, G: 0x6e, B: 0xff, A: 0xff}
	colorPrimaryDim  = color.NRGBA{R: 0x40, G: 0x4a, B: 0xcc, A: 0xff}
	colorPrimaryGlow = color.NRGBA{R: 0x63, G: 0x6e, B: 0xff, A: 0x28}
	colorOnPrimary   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// Accent – cyan spark
	colorAccent = color.NRGBA{R: 0x00, G: 0xd4, B: 0xff, A: 0xff}

	// Success / warning / error
	colorSuccess = color.NRGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}
	colorWarning = color.NRGBA{R: 0xff, G: 0xb8, B: 0x27, A: 0xff}
	colorError   = color.NRGBA{R: 0xff, G: 0x4d, B: 0x6d, A: 0xff}

	// Text
	colorFg         = color.NRGBA{R: 0xf0, G: 0xf2, B: 0xff, A: 0xff} // near-white, cool tint
	colorFgMuted    = color.NRGBA{R: 0x8b, G: 0x91, B: 0xb4, A: 0xff} // muted slate
	colorFgDisabled = color.NRGBA{R: 0x48, G: 0x4f, B: 0x6b, A: 0xff}

	// Inputs
	colorInputBg     = color.NRGBA{R: 0x1a, G: 0x1d, B: 0x28, A: 0xff}
	colorInputBorder = color.NRGBA{R: 0x30, G: 0x35, B: 0x4f, A: 0xff}

	// Hover
	colorHover     = color.NRGBA{R: 0x63, G: 0x6e, B: 0xff, A: 0x1a}
	colorSelection = color.NRGBA{R: 0x63, G: 0x6e, B: 0xff, A: 0x40}
	colorFocus     = color.NRGBA{R: 0x63, G: 0x6e, B: 0xff, A: 0x80}

	// Scrollbar
	colorScrollBar = color.NRGBA{R: 0x40, G: 0x46, B: 0x65, A: 0xff}

	// Shadow
	colorShadow = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x60}

	// Header
	colorHeader = color.NRGBA{R: 0x1a, G: 0x1d, B: 0x29, A: 0xff}
)

// ── Exported accent colors for use in custom widgets ───────────────────────────

// ColorPrimary is the electric indigo brand color.
var ColorPrimary = colorPrimary

// ColorAccent is the cyan spark accent.
var ColorAccent = colorAccent

// ColorSuccess is the success green.
var ColorSuccess = colorSuccess

// ColorWarning is the warning amber.
var ColorWarning = colorWarning

// ColorError is the error red.
var ColorError = colorError

// ColorSurface is the elevated surface color (cards, panels).
var ColorSurface = colorSurface2

// ColorSurface3 is the card/header color.
var ColorSurface3 = colorSurface3

// ColorForeground is the primary text color.
var ColorForeground = colorFg

// ColorFgMuted is the muted/secondary text color.
var ColorFgMuted = colorFgMuted

// ColorSeparator is the separator/border color.
var ColorSeparator = colorSeparator

// ColorInputBg is the input field background.
var ColorInputBg = colorInputBg

// ColorInputBorder is the input field border.
var ColorInputBorder = colorInputBorder

// ColorBackground is the app background.
var ColorBackground = colorBg

// ColorPrimaryGlow is a translucent glow used for decorative effects.
var ColorPrimaryGlow = colorPrimaryGlow

// ColorOnPrimary is the text color on primary-colored backgrounds.
var ColorOnPrimary = colorOnPrimary

// ── obsidianTheme ──────────────────────────────────────────────────────────────

// ObsidianTheme is the premium dark theme for Peeksy.
type ObsidianTheme struct{}

// Color returns the theme color for the given name and variant.
// We ignore variant because Obsidian is always dark.
func (t *ObsidianTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colorBg
	case theme.ColorNameButton:
		return colorSurface3
	case theme.ColorNameDisabledButton:
		return colorSurface2
	case theme.ColorNameDisabled:
		return colorFgDisabled
	case theme.ColorNameError:
		return colorError
	case theme.ColorNameForeground:
		return colorFg
	case theme.ColorNameForegroundOnError:
		return colorOnPrimary
	case theme.ColorNameForegroundOnPrimary:
		return colorOnPrimary
	case theme.ColorNameForegroundOnSuccess:
		return colorOnPrimary
	case theme.ColorNameForegroundOnWarning:
		return colorBg
	case theme.ColorNameFocus:
		return colorFocus
	case theme.ColorNameHeaderBackground:
		return colorHeader
	case theme.ColorNameHover:
		return colorHover
	case theme.ColorNameHyperlink:
		return colorAccent
	case theme.ColorNameInputBackground:
		return colorInputBg
	case theme.ColorNameInputBorder:
		return colorInputBorder
	case theme.ColorNameMenuBackground:
		return colorSurface2
	case theme.ColorNameOverlayBackground:
		return colorSurface
	case theme.ColorNamePlaceHolder:
		return colorFgMuted
	case theme.ColorNamePressed:
		return colorPrimaryDim
	case theme.ColorNamePrimary:
		return colorPrimary
	case theme.ColorNameScrollBar:
		return colorScrollBar
	case theme.ColorNameSelection:
		return colorSelection
	case theme.ColorNameSeparator:
		return colorSeparator
	case theme.ColorNameShadow:
		return colorShadow
	case theme.ColorNameSuccess:
		return colorSuccess
	case theme.ColorNameWarning:
		return colorWarning
	default:
		// Fallback: use built-in dark theme
		return theme.DarkTheme().Color(name, theme.VariantDark)
	}
}

// Font returns the font for the given text style.
// We use built-in fonts — Fyne bundles NotoSans which is clean and modern.
func (t *ObsidianTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

// Icon returns the icon for the given name.
func (t *ObsidianTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

// Size returns the size for the given size name.
func (t *ObsidianTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameText:
		return 13
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius:
		return 8
	case theme.SizeNameSelectionRadius:
		return 4
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 3
	case theme.SizeNameScrollBarRadius:
		return 5
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameLineSpacing:
		return 5
	default:
		return theme.DefaultTheme().Size(name)
	}
}
