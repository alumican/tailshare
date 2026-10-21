package qr

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Data codewords at level M, from the standard's capacity table
func TestCapacity(t *testing.T) {
	for version, want := range map[int]int{1: 16, 2: 28, 3: 44, 4: 64, 5: 86, 6: 108, 7: 124, 8: 154, 9: 182, 10: 216, 15: 415, 20: 669, 27: 1128, 40: 2334} {
		if got := dataCodewords(version); got != want {
			t.Errorf("version %d: %d data codewords, want %d", version, got, want)
		}
	}
}

func TestAlignmentPositions(t *testing.T) {
	for version, want := range map[int]string{1: "[]", 2: "[6 18]", 7: "[6 22 38]", 14: "[6 26 46 66]", 32: "[6 34 60 86 112 138]", 40: "[6 30 58 86 114 142 170]"} {
		if got := fmt.Sprint(alignmentPositions(version)); got != want {
			t.Errorf("version %d: %s, want %s", version, got, want)
		}
	}
}

func TestSizeAndFinders(t *testing.T) {
	for _, n := range []int{1, 15, 100, 1000} {
		code, err := Encode(strings.Repeat("x", n))
		if err != nil {
			t.Fatal(err)
		}
		if (code.Size-17)%4 != 0 {
			t.Errorf("%d bytes: size %d", n, code.Size)
		}
		// The three finder patterns: dark corners and centres, light rings
		for _, corner := range [][2]int{{3, 3}, {code.Size - 4, 3}, {3, code.Size - 4}} {
			x, y := corner[0], corner[1]
			if !code.Dark(x, y) || !code.Dark(x-3, y-3) || code.Dark(x-2, y) || code.Dark(x+2, y) {
				t.Errorf("%d bytes: no finder at %v", n, corner)
			}
		}
	}
	if _, err := Encode(strings.Repeat("x", 2332)); err != ErrTooLong {
		t.Errorf("2332 bytes: %v", err)
	}
}

// Encode texts of many lengths, across versions, and read them back with CoreImage (macOS only)
func TestReadBack(t *testing.T) {
	swift, err := exec.LookPath("swift")
	if runtime.GOOS != "darwin" || err != nil {
		t.Skip("reads QR codes with CoreImage, through swift, on macOS")
	}
	dir := t.TempDir()
	var texts, files []string
	base := "https://host.example.ts.net:10443/"
	for _, n := range []int{0, 1, 5, 20, 40, 60, 90, 120, 180, 250, 330, 450, 600, 800, 1000} {
		texts = append(texts, base+strings.Repeat("abcdefghij", n/10+1)[:n])
	}
	texts = append(texts, "a", "Hello, 世界", base+"My%20App.ipa?v=1234567890")
	for i, text := range texts {
		code, err := Encode(text)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, fmt.Sprintf("%02d.png", i))
		writePNG(t, code, file)
		files = append(files, file)
	}
	out, err := exec.Command(swift, append([]string{"testdata/read.swift"}, files...)...).Output()
	if err != nil {
		t.Skipf("swift could not run: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	for i, text := range texts {
		code, _ := Encode(text)
		if i >= len(lines) || lines[i] != text {
			got := "nothing"
			if i < len(lines) {
				got = lines[i]
			}
			t.Errorf("version %d, %d bytes: read %q", (code.Size-17)/4, len(text), got)
		}
	}
}

func writePNG(t *testing.T, code *Code, file string) {
	const scale, margin = 6, 4
	side := (code.Size + 2*margin) * scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			c := color.Gray{255}
			if code.Dark(x/scale-margin, y/scale-margin) {
				c = color.Gray{0}
			}
			img.SetGray(x, y, c)
		}
	}
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// The terminal drawing, read back module by module, is the code with its quiet zone
func TestTerminal(t *testing.T) {
	const margin = 2
	code, _ := Encode("https://host.example.ts.net:10443/a.txt")
	// Each character is two modules, top and bottom
	halves := map[rune][2]bool{' ': {false, false}, '▀': {true, false}, '▄': {false, true}, '█': {true, true}}
	lines := strings.Split(strings.TrimSuffix(code.Terminal(margin), "\n"), "\n")
	if len(lines) != (code.Size+2*margin+1)/2 {
		t.Fatalf("%d lines for size %d", len(lines), code.Size)
	}
	for row, line := range lines {
		if !strings.HasPrefix(line, "\x1b[30;47m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d is not black on white: %q", row, line)
		}
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;47m"), "\x1b[0m"))
		if len(cells) != code.Size+2*margin {
			t.Fatalf("line %d: %d cells", row, len(cells))
		}
		for col, cell := range cells {
			x, y := col-margin, row*2-margin
			half, ok := halves[cell]
			if !ok || half[0] != code.Dark(x, y) || half[1] != code.Dark(x, y+1) {
				t.Fatalf("cell %d,%d (%q) does not match modules %d,%d", col, row, cell, x, y)
			}
		}
	}
}
