// Copyright 2026 Tamás Gulácsi.
//
// SPDX-License-Identifier: LGPL-3.0

package vlup

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

func WriteJournalEntry(w io.Writer, priority int, message []byte, vars map[string]string) error {
	fmt.Fprintf(w, "PRIORITY=%d\n", priority)
	if err := appendVariable(w, "MESSAGE", message); err != nil {
		return err
	}
	for k, v := range vars {
		if err := appendVariable(w, k, []byte(v)); err != nil {
			return err
		}
	}
	return nil
}

func appendVariable(w io.Writer, name string, value []byte) error {
	// copied from https://github.com/coreos/go-systemd/blob/v22.7.0/journal/journal_unix.go#L188
	if !bytes.ContainsRune(value, '\n') {
		/* just write the variable and value all on one line */
		_, err := fmt.Fprintf(w, "%s=%s\n", name, value)
		return err
	}
	/* When the value contains a newline, we write:
	 * - the variable name, followed by a newline
	 * - the size (in 64bit little endian format)
	 * - the data, followed by a newline
	 */
	fmt.Fprintln(w, name)
	_ = binary.Write(w, binary.LittleEndian, uint64(len(value)))
	_, err := w.Write(value)
	w.Write([]byte{'\n'})
	return err
}

func CopyJournalEntry(w io.Writer, br *bufio.Reader) (int64, error) {
	var written int64
	W := func(p []byte) error {
		n, err := w.Write(p)
		written += int64(n)
		return err
	}
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			W(line)
			return written, fmt.Errorf("ReadSlice: %w", err)
		}

		// End-of-entry: blank line
		if len(line) == 1 && bytes.Equal(line, []byte{'\n'}) ||
			len(line) == 2 && bytes.Equal(line, []byte("\r\n")) {
			err := W(line)
			return written, err
		}

		trimmed := bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(trimmed, []byte("-- cursor: ")) {
			continue
		}

		// Text form: FIELD=value
		if i := bytes.IndexByte(trimmed, '='); i >= 0 {
			err := W(line)
			if err != nil {
				return written, err
			}
			continue
		}

		// Binary-safe form:
		// FIELD\n + 8-byte little-endian length + <data> + '\n'
		field := string(trimmed)
		var szBuf [8]byte
		if _, err2 := io.ReadFull(br, szBuf[:]); err2 != nil {
			return written, fmt.Errorf("read size for %q: %w", field, err2)
		}
		size := binary.LittleEndian.Uint64(szBuf[:])

		// Guardrail: avoid absurd allocations on corrupted input
		// (tune this as you like; journald fields are usually small)
		if size > uint64(br.Size()) {
			return written, fmt.Errorf("field %q too large: %d bytes", field, size)
		}
		if err := W([]byte(field + "\n")); err != nil {
			return written, err
		}
		if err := W(szBuf[:]); err != nil {
			return written, err
		}

		n, err := io.CopyN(w, br, int64(size))
		written += n
		if err != nil {
			return written, err
		}

		// Consume the trailing '\n' separator after the binary payload
		b, err2 := br.ReadByte()
		if err2 != nil {
			return written, fmt.Errorf("read newline after %q: %w", field, err2)
		}
		if b != '\n' {
			return written, fmt.Errorf("expected newline after %q data, got 0x%02x", field, b)
		}
		if err := W([]byte{b}); err != nil {
			return written, err
		}
	}
}
