// Package plist reads property lists, in the XML and the binary format, into plain Go values.
//
// It is enough to read an app's Info.plist: dictionaries become map[string]any, arrays []any,
// strings string, integers int64, reals float64, booleans bool, data []byte, and dates time.Time.
// It does not write property lists.
package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Decode reads a property list in either format.
func Decode(data []byte) (any, error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return decodeBinary(data)
	}
	return decodeXML(data)
}

// The reference date of property lists: dates are seconds since then
var epoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// MARK: - XML

func decodeXML(data []byte) (any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("plist: no <plist> element: %w", err)
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local != "plist" {
				return nil, fmt.Errorf("plist: unexpected <%s>", start.Name.Local)
			}
			value, end, err := xmlValue(decoder)
			if err != nil {
				return nil, err
			}
			if end {
				return nil, errors.New("plist: empty <plist>")
			}
			return value, nil
		}
	}
}

// The next value inside the current element; end is true when the element closes instead
func xmlValue(decoder *xml.Decoder) (value any, end bool, err error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, false, fmt.Errorf("plist: %w", err)
		}
		switch token := token.(type) {
		case xml.EndElement:
			return nil, true, nil
		case xml.StartElement:
			value, err := xmlElement(decoder, token)
			return value, false, err
		}
	}
}

func xmlElement(decoder *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "dict":
		dict := map[string]any{}
		for {
			key, end, err := xmlValue(decoder)
			if err != nil {
				return nil, err
			}
			if end {
				return dict, nil
			}
			name, ok := key.(xmlKey)
			if !ok {
				return nil, errors.New("plist: a <dict> entry without <key>")
			}
			value, end, err := xmlValue(decoder)
			if err != nil {
				return nil, err
			}
			if end {
				return nil, fmt.Errorf("plist: no value for key %q", name)
			}
			dict[string(name)] = value
		}
	case "array":
		array := []any{}
		for {
			value, end, err := xmlValue(decoder)
			if err != nil {
				return nil, err
			}
			if end {
				return array, nil
			}
			array = append(array, value)
		}
	case "true", "false":
		if err := decoder.Skip(); err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		return start.Name.Local == "true", nil
	}

	var text string
	if err := decoder.DecodeElement(&text, &start); err != nil {
		return nil, fmt.Errorf("plist: %w", err)
	}
	switch start.Name.Local {
	case "key":
		return xmlKey(text), nil
	case "string":
		return text, nil
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	case "data":
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(text))
	}
	return nil, fmt.Errorf("plist: unknown element <%s>", start.Name.Local)
}

type xmlKey string

// MARK: - Binary

type binaryPlist struct {
	data      []byte
	offsets   []uint64
	refSize   int
	remaining int // objects that may still be decoded, so a crafted file cannot loop for ever
}

func decodeBinary(data []byte) (any, error) {
	if len(data) < 8+32 {
		return nil, errors.New("plist: binary plist too short")
	}
	trailer := data[len(data)-32:]
	offsetSize := int(trailer[6])
	refSize := int(trailer[7])
	count := binary.BigEndian.Uint64(trailer[8:])
	top := binary.BigEndian.Uint64(trailer[16:])
	tableOffset := binary.BigEndian.Uint64(trailer[24:])
	if offsetSize < 1 || offsetSize > 8 || refSize < 1 || refSize > 8 || top >= count ||
		count > uint64(len(data)) || tableOffset+count*uint64(offsetSize) > uint64(len(data)-32) {
		return nil, errors.New("plist: binary plist has a broken trailer")
	}
	p := &binaryPlist{data: data, refSize: refSize, remaining: 1 << 16}
	p.offsets = make([]uint64, count)
	for i := range p.offsets {
		start := tableOffset + uint64(i*offsetSize)
		p.offsets[i] = readUint(data[start : start+uint64(offsetSize)])
	}
	return p.object(top, 0)
}

func readUint(b []byte) uint64 {
	var n uint64
	for _, c := range b {
		n = n<<8 | uint64(c)
	}
	return n
}

func (p *binaryPlist) object(ref uint64, depth int) (any, error) {
	p.remaining--
	if ref >= uint64(len(p.offsets)) || depth > 64 || p.remaining < 0 {
		return nil, errors.New("plist: binary plist refers past its objects")
	}
	offset := p.offsets[ref]
	if offset >= uint64(len(p.data)) {
		return nil, errors.New("plist: binary plist object out of range")
	}
	marker := p.data[offset]
	kind, info := marker>>4, int(marker&0x0f)
	body := offset + 1

	switch kind {
	case 0x0:
		switch info {
		case 0x8:
			return false, nil
		case 0x9:
			return true, nil
		}
		return nil, nil
	case 0x1:
		size := uint64(1) << info
		b, err := p.bytes(body, size)
		if err != nil {
			return nil, err
		}
		if size == 16 {
			b = b[8:]
		}
		return int64(readUint(b)), nil
	case 0x2:
		b, err := p.bytes(body, uint64(1)<<info)
		if err != nil {
			return nil, err
		}
		switch len(b) {
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
		}
		return nil, errors.New("plist: unsupported real size")
	case 0x3:
		b, err := p.bytes(body, 8)
		if err != nil {
			return nil, err
		}
		seconds := math.Float64frombits(binary.BigEndian.Uint64(b))
		return epoch.Add(time.Duration(seconds * float64(time.Second))), nil
	}

	length, start, err := p.length(info, body)
	if err != nil {
		return nil, err
	}
	switch kind {
	case 0x4:
		b, err := p.bytes(start, length)
		return append([]byte(nil), b...), err
	case 0x5:
		b, err := p.bytes(start, length)
		return string(b), err
	case 0x6:
		b, err := p.bytes(start, length*2)
		if err != nil {
			return nil, err
		}
		units := make([]uint16, length)
		for i := range units {
			units[i] = binary.BigEndian.Uint16(b[i*2:])
		}
		return string(utf16.Decode(units)), nil
	case 0x8:
		b, err := p.bytes(body, uint64(info)+1)
		return int64(readUint(b)), err
	case 0xa, 0xc:
		refs, err := p.refs(start, length)
		if err != nil {
			return nil, err
		}
		array := make([]any, 0, len(refs))
		for _, r := range refs {
			value, err := p.object(r, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		return array, nil
	case 0xd:
		refs, err := p.refs(start, length*2)
		if err != nil {
			return nil, err
		}
		dict := make(map[string]any, length)
		for i := uint64(0); i < length; i++ {
			key, err := p.object(refs[i], depth+1)
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("plist: a dictionary key that is not a string")
			}
			value, err := p.object(refs[length+i], depth+1)
			if err != nil {
				return nil, err
			}
			dict[name] = value
		}
		return dict, nil
	}
	return nil, fmt.Errorf("plist: unknown object type 0x%x", marker)
}

// The length of a string, data, array or dictionary, and where its contents start
func (p *binaryPlist) length(info int, body uint64) (length, start uint64, err error) {
	if info != 0x0f {
		return uint64(info), body, nil
	}
	b, err := p.bytes(body, 1)
	if err != nil {
		return 0, 0, err
	}
	if b[0]>>4 != 0x1 {
		return 0, 0, errors.New("plist: binary plist has a broken length")
	}
	size := uint64(1) << (b[0] & 0x0f)
	n, err := p.bytes(body+1, size)
	if err != nil {
		return 0, 0, err
	}
	return readUint(n), body + 1 + size, nil
}

func (p *binaryPlist) refs(start, count uint64) ([]uint64, error) {
	b, err := p.bytes(start, count*uint64(p.refSize))
	if err != nil {
		return nil, err
	}
	refs := make([]uint64, count)
	for i := range refs {
		refs[i] = readUint(b[i*p.refSize : (i+1)*p.refSize])
	}
	return refs, nil
}

func (p *binaryPlist) bytes(start, size uint64) ([]byte, error) {
	if size > uint64(len(p.data)) || start > uint64(len(p.data))-size {
		return nil, io.ErrUnexpectedEOF
	}
	return p.data[start : start+size], nil
}
