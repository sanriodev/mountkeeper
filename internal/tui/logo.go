package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// The logo of assets/logo.svg in box characters: two peaks over the network
// line, the name beside them. The main menu shows it while the window has
// room for it and all the menu's items.
var (
	logoMark = []string{
		`     ╱╲`,
		`    ╱  ╲  ╱╲`,
		`   ╱    ╲╱  ╲`,
		`━━●━━━━●━━━━●━━`,
	}
	logoName = []string{
		`┏┳┓┏━┓╻ ╻┏┓╻╺┳╸╻┏ ┏━╸┏━╸┏━┓┏━╸┏━┓`,
		`┃┃┃┃ ┃┃ ┃┃┗┫ ┃ ┣┻┓┣╸ ┣╸ ┣━┛┣╸ ┣┳┛`,
		`╹ ╹┗━┛┗━┛╹ ╹ ╹ ╹ ╹┗━╸┗━╸╹  ┗━╸╹┗╸`,
	}
	logoTagline = "network shares, kept within reach"

	// The colours of the SVG's gradient, and darker ones for light terminals.
	logoDark  = []string{"#38bdf8", "#818cf8", "#c084fc"}
	logoLight = []string{"#0284c7", "#4f46e5", "#9333ea"}
)

const logoGap = 18 // column where the name starts

var (
	logoOnce     sync.Once
	logoRendered string
)

// logo returns the coloured logo, one line per row, without indentation.
func logo() string {
	logoOnce.Do(func() { logoRendered = renderLogo() })
	return logoRendered
}

func renderLogo() string {
	rows := make([]string, len(logoMark))
	for i := range rows {
		row := fmt.Sprintf("%-*s", logoGap, logoMark[i])
		if n := i - (len(logoMark) - len(logoName)); n >= 0 {
			row += logoName[n]
		}
		rows[i] = row
	}
	width := 0
	for _, r := range rows {
		width = max(width, len([]rune(r)))
	}

	var b strings.Builder
	for _, row := range rows {
		for col, r := range []rune(row) {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			t := float64(col) / float64(max(1, width-1))
			color := lipgloss.AdaptiveColor{Light: gradient(logoLight, t), Dark: gradient(logoDark, t)}
			b.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(r)))
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat(" ", logoGap) + helpStyle.Render(logoTagline))
	return b.String()
}

// gradient returns the colour at t (0..1) on a line through the given stops.
func gradient(stops []string, t float64) string {
	t = max(0, min(1, t)) * float64(len(stops)-1)
	i := min(int(t), len(stops)-2)
	from, to := parseHex(stops[i]), parseHex(stops[i+1])
	f := t - float64(i)
	var mixed [3]int
	for c := range mixed {
		mixed[c] = int(float64(from[c]) + (float64(to[c])-float64(from[c]))*f + 0.5)
	}
	return fmt.Sprintf("#%02x%02x%02x", mixed[0], mixed[1], mixed[2])
}

func parseHex(s string) (rgb [3]int) {
	fmt.Sscanf(s, "#%02x%02x%02x", &rgb[0], &rgb[1], &rgb[2])
	return rgb
}
