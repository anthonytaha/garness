package sandbox

import (
	"bufio"
	"fmt"
	"io"
)

// CONNECT is HTTPS-only. Check the first TLS record's SNI to prevent tunnels
// carrying arbitrary protocols or selecting a different virtual host.
func readClientHello(reader *bufio.Reader, host string) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	size := int(header[3])<<8 | int(header[4])
	if header[0] != 22 || header[1] != 3 || size < 42 || size > 16384 {
		return nil, fmt.Errorf("TLS ClientHello required")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	if data[0] != 1 || int(data[1])<<16|int(data[2])<<8|int(data[3]) != len(data)-4 {
		return nil, fmt.Errorf("complete ClientHello required")
	}
	pos := 38
	take := func(n int) ([]byte, bool) {
		if n < 0 || pos+n > len(data) {
			return nil, false
		}
		value := data[pos : pos+n]
		pos += n
		return value, true
	}
	value, ok := take(1)
	if !ok {
		return nil, fmt.Errorf("invalid TLS hello")
	}
	if _, ok = take(int(value[0])); !ok {
		return nil, fmt.Errorf("invalid session")
	}
	value, ok = take(2)
	if !ok {
		return nil, fmt.Errorf("invalid ciphers")
	}
	if _, ok = take(int(value[0])<<8 | int(value[1])); !ok {
		return nil, fmt.Errorf("invalid ciphers")
	}
	value, ok = take(1)
	if !ok {
		return nil, fmt.Errorf("invalid compression")
	}
	if _, ok = take(int(value[0])); !ok {
		return nil, fmt.Errorf("invalid compression")
	}
	value, ok = take(2)
	if !ok || int(value[0])<<8|int(value[1]) != len(data)-pos {
		return nil, fmt.Errorf("invalid extensions")
	}
	matched := false
	for pos < len(data) {
		value, ok = take(4)
		if !ok {
			break
		}
		kind, length := int(value[0])<<8|int(value[1]), int(value[2])<<8|int(value[3])
		extension, valid := take(length)
		if !valid {
			break
		}
		if kind == 0xfe0d {
			return nil, fmt.Errorf("encrypted ClientHello is not allowed")
		}
		if kind == 0 {
			if matched || len(extension) < 5 || int(extension[0])<<8|int(extension[1]) != len(extension)-2 || extension[2] != 0 || int(extension[3])<<8|int(extension[4]) != len(extension)-5 || string(extension[5:]) != host {
				return nil, fmt.Errorf("TLS SNI must match approved hostname")
			}
			matched = true
		}
	}
	if matched && pos == len(data) {
		return append(header, data...), nil
	}
	return nil, fmt.Errorf("TLS SNI must match approved hostname")
}
