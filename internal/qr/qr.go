// Package qr makes QR codes for short texts such as URLs, to show in a terminal.
//
// It encodes bytes (byte mode) at error correction level M, in the smallest version from 1 to 40
// that holds them, following ISO/IEC 18004. It only encodes; reading is left to phones.
package qr

import (
	"errors"
	"strings"
)

// Code is a QR code: Size×Size modules, true for dark.
type Code struct {
	Size    int
	modules [][]bool
}

// Dark tells whether the module at column x, row y is dark.
func (c *Code) Dark(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.modules[y][x]
}

// Error correction level M: about 15% of the code can be damaged and still read
var (
	eccPerBlock = [41]int{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28}
	numBlocks   = [41]int{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49}
)

const formatBitsM = 0 // level M in the format information

var ErrTooLong = errors.New("qr: text too long for a QR code")

// Encode makes the QR code for text.
func Encode(text string) (*Code, error) {
	data := []byte(text)
	version := 0
	for v := 1; v <= 40; v++ {
		if 4+countBits(v)+8*len(data) <= dataCodewords(v)*8 {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, ErrTooLong
	}

	// The bit stream: byte mode, the length, the bytes, a terminator, then padding to capacity
	var bits bitBuffer
	bits.append(0b0100, 4)
	bits.append(len(data), countBits(version))
	for _, b := range data {
		bits.append(int(b), 8)
	}
	capacity := dataCodewords(version) * 8
	bits.append(0, min(4, capacity-len(bits)))
	bits.append(0, (8-len(bits)%8)%8)
	for pad := 0xec; len(bits) < capacity; pad ^= 0xec ^ 0x11 {
		bits.append(pad, 8)
	}
	codewords := make([]byte, len(bits)/8)
	for i, bit := range bits {
		codewords[i/8] |= bit << (7 - i%8)
	}

	c := newCode(version)
	c.drawCodewords(addErrorCorrection(version, codewords))
	c.applyBestMask()
	return &c.Code, nil
}

func countBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

// Modules that carry data and error correction, in a version's symbol
func rawDataModules(version int) int {
	result := (16*version+128)*version + 64
	if version >= 2 {
		align := version/7 + 2
		result -= (25*align-10)*align - 55
		if version >= 7 {
			result -= 36
		}
	}
	return result
}

func dataCodewords(version int) int {
	return rawDataModules(version)/8 - eccPerBlock[version]*numBlocks[version]
}

type bitBuffer []byte

func (b *bitBuffer) append(value, length int) {
	for i := length - 1; i >= 0; i-- {
		*b = append(*b, byte(value>>i&1))
	}
}

// MARK: - Error correction

// Split the data into blocks, add Reed-Solomon error correction to each, and interleave them
func addErrorCorrection(version int, data []byte) []byte {
	blocks := numBlocks[version]
	eccLen := eccPerBlock[version]
	raw := rawDataModules(version) / 8
	shortBlocks := blocks - raw%blocks
	shortLen := raw / blocks
	divisor := reedSolomonDivisor(eccLen)

	var all [][]byte
	for i, k := 0, 0; i < blocks; i++ {
		length := shortLen - eccLen
		if i >= shortBlocks {
			length++
		}
		block := append([]byte(nil), data[k:k+length]...)
		k += length
		ecc := reedSolomonRemainder(block, divisor)
		if i < shortBlocks {
			// A placeholder, so short and long blocks interleave by the same index
			block = append(block, 0)
		}
		all = append(all, append(block, ecc...))
	}

	result := make([]byte, 0, raw)
	for i := range all[0] {
		for j, block := range all {
			if i != shortLen-eccLen || j >= shortBlocks {
				result = append(result, block[i])
			}
		}
	}
	return result
}

func reedSolomonDivisor(degree int) []byte {
	result := make([]byte, degree)
	result[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range result {
			result[j] = gfMultiply(result[j], root)
			if j+1 < len(result) {
				result[j] ^= result[j+1]
			}
		}
		root = gfMultiply(root, 0x02)
	}
	return result
}

func reedSolomonRemainder(data, divisor []byte) []byte {
	result := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i, d := range divisor {
			result[i] ^= gfMultiply(d, factor)
		}
	}
	return result
}

// Multiplication in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1
func gfMultiply(x, y byte) byte {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11d)
		z ^= int(y>>i&1) * int(x)
	}
	return byte(z)
}

// MARK: - Drawing

type builder struct {
	Code
	version  int
	function [][]bool // modules that are not data: finders, timing, alignment, format and version
}

func newCode(version int) *builder {
	size := version*4 + 17
	b := &builder{Code: Code{Size: size}, version: version}
	b.modules = make([][]bool, size)
	b.function = make([][]bool, size)
	for i := range b.modules {
		b.modules[i] = make([]bool, size)
		b.function[i] = make([]bool, size)
	}

	for i := 0; i < size; i++ {
		b.set(6, i, i%2 == 0)
		b.set(i, 6, i%2 == 0)
	}
	b.finder(3, 3)
	b.finder(size-4, 3)
	b.finder(3, size-4)
	positions := alignmentPositions(version)
	last := len(positions) - 1
	for i, x := range positions {
		for j, y := range positions {
			if !(i == 0 && j == 0 || i == 0 && j == last || i == last && j == 0) {
				b.alignment(x, y)
			}
		}
	}
	b.drawFormat(0) // reserves the format modules; drawn for real once the mask is chosen
	b.drawVersion()
	return b
}

func (b *builder) set(x, y int, dark bool) {
	b.modules[y][x] = dark
	b.function[y][x] = true
}

// A finder pattern centred on x, y, with its light separator
func (b *builder) finder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			distance := max(abs(dx), abs(dy))
			if xx, yy := x+dx, y+dy; xx >= 0 && xx < b.Size && yy >= 0 && yy < b.Size {
				b.set(xx, yy, distance != 2 && distance != 4)
			}
		}
	}
}

func (b *builder) alignment(x, y int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			b.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

// Where alignment patterns are centred, the same list for rows and columns: 6, then evenly spaced
// up to 6 modules from the far edge
func alignmentPositions(version int) []int {
	if version == 1 {
		return nil
	}
	count := version/7 + 2
	step := 26
	if version != 32 {
		step = (version*4 + count*2 + 1) / (count*2 - 2) * 2
	}
	positions := make([]int, count)
	positions[0] = 6
	for i, pos := count-1, version*4+10; i >= 1; i, pos = i-1, pos-step {
		positions[i] = pos
	}
	return positions
}

func (b *builder) drawFormat(mask int) {
	data := formatBitsM<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 != 0 }

	for i := 0; i <= 5; i++ {
		b.set(8, i, bit(i))
	}
	b.set(8, 7, bit(6))
	b.set(8, 8, bit(7))
	b.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		b.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		b.set(b.Size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		b.set(8, b.Size-15+i, bit(i))
	}
	b.set(8, b.Size-8, true)
}

func (b *builder) drawVersion() {
	if b.version < 7 {
		return
	}
	rem := b.version
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1f25)
	}
	bits := b.version<<12 | rem
	for i := 0; i < 18; i++ {
		dark := bits>>i&1 != 0
		x, y := b.Size-11+i%3, i/3
		b.set(x, y, dark)
		b.set(y, x, dark)
	}
}

// Place the codewords in the zigzag order, two columns at a time from the right
func (b *builder) drawCodewords(data []byte) {
	i := 0
	for right := b.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < b.Size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = b.Size - 1 - vert
				}
				if !b.function[y][x] && i < len(data)*8 {
					b.modules[y][x] = data[i>>3]>>(7-i&7)&1 != 0
					i++
				}
			}
		}
	}
}

// MARK: - Masking

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (b *builder) applyMask(mask int) {
	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			if !b.function[y][x] && maskBit(mask, x, y) {
				b.modules[y][x] = !b.modules[y][x]
			}
		}
	}
}

// Try the eight masks and keep the one with the lowest penalty, as the standard asks
func (b *builder) applyBestMask() {
	best, bestPenalty := 0, -1
	for mask := 0; mask < 8; mask++ {
		b.applyMask(mask)
		b.drawFormat(mask)
		if penalty := b.penalty(); bestPenalty < 0 || penalty < bestPenalty {
			best, bestPenalty = mask, penalty
		}
		b.applyMask(mask) // masking twice undoes it
	}
	b.applyMask(best)
	b.drawFormat(best)
}

func (b *builder) penalty() int {
	size := b.Size
	at := func(x, y int, transpose bool) bool {
		if transpose {
			return b.modules[x][y]
		}
		return b.modules[y][x]
	}
	result := 0
	for _, transpose := range []bool{false, true} {
		for y := 0; y < size; y++ {
			// Runs of five or more modules of one colour
			run := 1
			for x := 1; x <= size; x++ {
				if x < size && at(x, y, transpose) == at(x-1, y, transpose) {
					run++
					continue
				}
				if run >= 5 {
					result += run - 2
				}
				run = 1
			}
			// Patterns that look like a finder: 1011101 with four light modules on one side
			for x := 0; x+7 <= size; x++ {
				if !(at(x, y, transpose) && !at(x+1, y, transpose) && at(x+2, y, transpose) && at(x+3, y, transpose) &&
					at(x+4, y, transpose) && !at(x+5, y, transpose) && at(x+6, y, transpose)) {
					continue
				}
				light := func(from int) bool {
					for i := from; i < from+4; i++ {
						if i >= 0 && i < size && at(i, y, transpose) {
							return false
						}
					}
					return true
				}
				if light(x-4) || light(x+7) {
					result += 40
				}
			}
		}
	}
	dark := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if b.modules[y][x] {
				dark++
			}
			if x+1 < size && y+1 < size {
				c := b.modules[y][x]
				if c == b.modules[y][x+1] && c == b.modules[y+1][x] && c == b.modules[y+1][x+1] {
					result += 3
				}
			}
		}
	}
	total := size * size
	k := (abs(dark*20-total*10) + total - 1) / total
	return result + max(0, k-1)*10
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// MARK: - Showing

// Terminal draws the code with half blocks, two rows of modules per line, in black on white
// whatever the terminal's colours, with a quiet zone of margin modules around it.
func (c *Code) Terminal(margin int) string {
	var out strings.Builder
	for y := -margin; y < c.Size+margin; y += 2 {
		out.WriteString("\x1b[30;47m")
		for x := -margin; x < c.Size+margin; x++ {
			top, bottom := c.Dark(x, y), c.Dark(x, y+1)
			switch {
			case top && bottom:
				out.WriteString("█")
			case top:
				out.WriteString("▀")
			case bottom:
				out.WriteString("▄")
			default:
				out.WriteString(" ")
			}
		}
		out.WriteString("\x1b[0m\n")
	}
	return out.String()
}
