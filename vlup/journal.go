// Copyright 2026 Tamás Gulácsi.
//
// SPDX-License-Identifier: LGPL-3.0

package vlup

import (
	"bufio"
	"io"

	"github.com/tgulacsi/go/journal"
)

func WriteJournalEntry(w io.Writer, priority int, message []byte, vars map[string]string) error {
	_, err := journal.Record{Priority: uint8(priority), Message: string(message), Fields: vars}.WriteTo(w)
	return err
}

func CopyJournalEntry(w io.Writer, br *bufio.Reader) (int64, error) {
	return journal.CopyJournalRecord(w, br)
}
