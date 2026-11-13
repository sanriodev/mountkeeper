package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

var (
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
)

// interactive reports whether we can talk to a person: both stdin and stdout
// are terminals.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func success(format string, a ...any) {
	fmt.Println(okStyle.Render("✓") + " " + fmt.Sprintf(format, a...))
}

func warn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, warnStyle.Render("!")+" "+fmt.Sprintf(format, a...))
}

var stdin = bufio.NewReader(os.Stdin)

// ask prompts for one line; an empty answer yields def.
func ask(label, def string) (string, error) {
	if def != "" {
		fmt.Printf("%s %s: ", label, dimStyle.Render("["+def+"]"))
	} else {
		fmt.Printf("%s: ", label)
	}
	line, err := stdin.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		fmt.Println()
		return "", errors.New("input cancelled")
	}
	if line = strings.TrimSpace(line); line == "" {
		return def, nil
	}
	return line, nil
}

// confirm asks a yes/no question; an empty answer yields def.
func confirm(label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		answer, err := ask(label+" "+dimStyle.Render("("+hint+")"), "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "y", "yes", "j", "ja":
			return true, nil
		case "n", "no", "nein":
			return false, nil
		}
	}
}

// askPassword reads a password from the terminal without echoing it.
func askPassword(label string) (string, error) {
	fmt.Printf("%s: ", label)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", errors.New("input cancelled")
	}
	return string(pw), nil
}

// table prints rows in aligned columns. Cells may contain ANSI styling.
func table(rows [][]string) {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+3))
			}
		}
		fmt.Println("  " + strings.TrimRight(b.String(), " "))
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
