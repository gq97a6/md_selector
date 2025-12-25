package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
)

type item struct {
	name    string
	checked bool
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s <path-with-markdown-files>\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	target := flag.Arg(0)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		exitErr(err)
	}

	items, err := listMarkdown(absTarget)
	if err != nil {
		exitErr(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		exitErr(err)
	}

	items, err = applyPreviousSelections(items, cwd)
	if err != nil {
		exitErr(err)
	}

	if len(items) == 0 {
		fmt.Println("No Markdown files found in", absTarget)
		return
	}

	finalItems, aborted, err := runSelector(items)
	if err != nil {
		exitErr(err)
	}
	if aborted {
		fmt.Println("Selection aborted.")
		return
	}

	count, err := writeSelections(finalItems, cwd)
	if err != nil {
		exitErr(err)
	}

	if count == 0 {
		fmt.Println("Wrote empty selection to output.txt")
	} else {
		fmt.Printf("Saved %d selection(s) to output.txt\n", count)
	}
}

func runSelector(initial []item) ([]item, bool, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, false, err
	}
	if err := screen.Init(); err != nil {
		return nil, false, err
	}
	defer screen.Fini()

	items := make([]item, len(initial))
	copy(items, initial)

	screen.SetStyle(tcell.StyleDefault)

	instructions := "↑/↓ move • space toggle • enter save • q/Esc cancel"
	cursor := 0
	offset := 0

	for {
		viewHeight := drawScreen(screen, items, cursor, offset, instructions)
		ev := screen.PollEvent()
		switch event := ev.(type) {
		case *tcell.EventKey:
			switch event.Key() {
			case tcell.KeyEscape, tcell.KeyCtrlC:
				return nil, true, nil
			case tcell.KeyUp:
				if cursor > 0 {
					cursor--
				}
			case tcell.KeyDown:
				if cursor < len(items)-1 {
					cursor++
				}
			case tcell.KeyEnter:
				return items, false, nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'q', 'Q':
					return nil, true, nil
				case 'k':
					if cursor > 0 {
						cursor--
					}
				case 'j':
					if cursor < len(items)-1 {
						cursor++
					}
				case ' ':
					if len(items) > 0 {
						items[cursor].checked = !items[cursor].checked
					}
				}
			}
			offset = ensureVisible(cursor, offset, viewHeight, len(items))
		case *tcell.EventResize:
			screen.Sync()
		}
	}
}

func drawScreen(screen tcell.Screen, items []item, cursor, offset int, instructions string) int {
	screen.Clear()
	width, height := screen.Size()
	headerLines := 2
	viewHeight := height - headerLines
	if viewHeight < 1 {
		viewHeight = 1
	}

	drawLine(screen, 0, instructions, tcell.StyleDefault)
	drawLine(screen, 1, strings.Repeat("-", max(0, width)), tcell.StyleDefault)

	if len(items) == 0 {
		drawLine(screen, 2, "No Markdown files found.", tcell.StyleDefault)
		screen.Show()
		return viewHeight
	}

	if offset > len(items)-viewHeight {
		offset = max(0, len(items)-viewHeight)
	}
	end := offset + viewHeight
	if end > len(items) {
		end = len(items)
	}

	row := headerLines
	for i := offset; i < end; i++ {
		it := items[i]
		indicator := " "
		if cursor == i {
			indicator = ">"
		}
		box := "[ ]"
		if it.checked {
			box = "[x]"
		}
		line := fmt.Sprintf("%s %s %s", indicator, box, it.name)
		drawLine(screen, row, line, tcell.StyleDefault)
		row++
	}

	screen.Show()
	return viewHeight
}

func ensureVisible(cursor, offset, viewHeight, total int) int {
	if viewHeight <= 0 {
		return 0
	}
	maxOffset := max(0, total-viewHeight)
	if cursor < offset {
		offset = cursor
	} else if cursor >= offset+viewHeight {
		offset = cursor - viewHeight + 1
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func drawLine(screen tcell.Screen, row int, text string, style tcell.Style) {
	width, _ := screen.Size()
	runes := []rune(text)
	for col := 0; col < width; col++ {
		ch := ' '
		if col < len(runes) {
			ch = runes[col]
		}
		screen.SetContent(col, row, ch, nil, style)
	}
}

func listMarkdown(dir string) ([]item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var items []item
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem == "" {
			continue
		}
		items = append(items, item{name: stem})
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.Compare(strings.ToLower(items[i].name), strings.ToLower(items[j].name)) < 0
	})

	return items, nil
}

func applyPreviousSelections(items []item, cwd string) ([]item, error) {
	path := filepath.Join(cwd, "output.txt")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return items, nil
		}
		return nil, err
	}
	defer file.Close()

	index := make(map[string]int, len(items))
	for i, it := range items {
		index[it.name] = i
	}

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		entry := strings.TrimSpace(scanner.Text())
		if entry == "" {
			continue
		}
		idx, ok := index[entry]
		if !ok {
			return nil, fmt.Errorf("output.txt entry %q not found among markdown files", entry)
		}
		items[idx].checked = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func writeSelections(items []item, cwd string) (int, error) {
	var b strings.Builder
	count := 0
	for _, it := range items {
		if !it.checked {
			continue
		}
		b.WriteString(it.name)
		b.WriteByte('\n')
		count++
	}

	path := filepath.Join(cwd, "output.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return 0, err
	}
	return count, nil
}

func exitErr(err error) {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		fmt.Fprintf(os.Stderr, "Path error: %v\n", pathErr)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	os.Exit(1)
}
