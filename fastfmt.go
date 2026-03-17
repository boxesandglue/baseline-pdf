package pdf

import "io"

const hexDigits = "0123456789abcdef"

// byteWriter is the interface shared by strings.Builder and bytes.Buffer.
type byteWriter interface {
	io.Writer
	WriteByte(byte) error
	WriteString(string) (int, error)
}

// writeHex4 writes a uint16 as 4-digit lowercase hex.
func writeHex4(b byteWriter, v uint16) {
	b.WriteByte(hexDigits[v>>12&0xf])
	b.WriteByte(hexDigits[v>>8&0xf])
	b.WriteByte(hexDigits[v>>4&0xf])
	b.WriteByte(hexDigits[v&0xf])
}

// writeHex4Upper writes a uint16 as 4-digit uppercase hex.
func writeHex4Upper(b byteWriter, v uint16) {
	const digits = "0123456789ABCDEF"
	b.WriteByte(digits[v>>12&0xf])
	b.WriteByte(digits[v>>8&0xf])
	b.WriteByte(digits[v>>4&0xf])
	b.WriteByte(digits[v&0xf])
}

// writeZeroPadded10 writes an integer as a 10-digit zero-padded decimal.
func writeZeroPadded10(b byteWriter, v int) {
	var buf [10]byte
	for i := 9; i >= 0; i-- {
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	b.Write(buf[:])
}

// writeSpaces writes n space characters.
func writeSpaces(b byteWriter, n int) {
	const spaces = "                " // 16 spaces
	for n > len(spaces) {
		b.WriteString(spaces)
		n -= len(spaces)
	}
	if n > 0 {
		b.WriteString(spaces[:n])
	}
}

// writeInt writes an integer as decimal.
func writeInt(b byteWriter, v int) {
	var buf [20]byte
	neg := v < 0
	if neg {
		v = -v
	}
	i := len(buf) - 1
	for v >= 10 {
		buf[i] = byte('0' + v%10)
		v /= 10
		i--
	}
	buf[i] = byte('0' + v)
	if neg {
		i--
		buf[i] = '-'
	}
	b.Write(buf[i:])
}
