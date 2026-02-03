package remotehttp

import (
	"bufio"
	"io"
	"strings"
)

type sseEvent struct {
	Name string
	Data string
}

type sseDecoder struct {
	scanner *bufio.Scanner
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(r)
	// Allow larger payloads for results/tool calls.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &sseDecoder{scanner: scanner}
}

func (d *sseDecoder) next() (sseEvent, error) {
	var (
		name    string
		builder strings.Builder
	)

	for {
		if !d.scanner.Scan() {
			if err := d.scanner.Err(); err != nil {
				return sseEvent{}, err
			}
			if builder.Len() > 0 || name != "" {
				return sseEvent{Name: name, Data: builder.String()}, nil
			}
			return sseEvent{}, io.EOF
		}

		line := d.scanner.Text()
		if line == "" {
			if builder.Len() == 0 && name == "" {
				continue
			}
			return sseEvent{Name: name, Data: builder.String()}, nil
		}

		if strings.HasPrefix(line, "event:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(data)
			continue
		}
	}
}
