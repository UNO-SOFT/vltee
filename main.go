// Copyright 2026 Tamás Gulácsi.
//
// SPDX-License-Identifier: LGPL-3.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/UNO-SOFT/vltee/vlup"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
)

func main() {
	if err := Main(); err != nil {
		slog.Error("Main", "error", err)
		os.Exit(1)
	}
}

func Main() error {
	FS := ff.NewFlagSet("vltail")
	flagAccountID := FS.UintLong("account-id", uint(vlup.NewAccountID("")), "accountID")
	flagProjectID := FS.UintLong("project-id", uint(vlup.NewProjectID("")), "projectID")
	flagPriority := FS.IntLong("priority", 6, "priority")
	appCmd := ff.Command{Name: "vltail", Flags: FS,
		ShortHelp: "vltail [options] <vl-url>",
		Exec: func(ctx context.Context, args []string) error {
			if len(args) < 1 {
				return errors.New("destURL is required")
			}
			cl, err := vlup.NewClient(args[0], vlup.AccountID(*flagAccountID), vlup.ProjectID(*flagProjectID), nil)
			if err != nil {
				return err
			}
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 1<<20), 1<<20)
			vars := map[string]string{"LINE": ""}

			var mu sync.Mutex
			var buf bytes.Buffer
			done := make(chan struct{})
			ticker := time.NewTicker(time.Second)
			var wg sync.WaitGroup
			wg.Go(func() {
				for {
					var exit bool
					select {
					case <-ticker.C:
					case <-done:
						exit = true
					case <-ctx.Done():
						exit = true
					}
					mu.Lock()
					if err := cl.UploadJournal(ctx, buf.Bytes()); err != nil {
						slog.Error("UploadJournal", "error", err)
					}
					buf.Reset()
					mu.Unlock()
					if exit {
						return
					}
				}
			})
			var line int64
			for scanner.Scan() {
				line++
				vars["LINE"] = strconv.FormatInt(line, 10)
				mu.Lock()
				err := vlup.WriteJournalEntry(&buf, *flagPriority, scanner.Bytes(), vars)
				mu.Unlock()
				if err != nil {
					slog.Error("WriteJournalEntry", "error", err)
				}
				os.Stdout.Write(append(scanner.Bytes(), '\n'))
			}
			close(done)
			wg.Wait()
			return nil
		},
	}

	if err := appCmd.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, ff.ErrHelp) {
			ffhelp.Command(&appCmd).WriteTo(os.Stderr)
			return nil
		}
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return appCmd.Run(ctx)
}
