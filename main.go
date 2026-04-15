// Copyright 2026 Tamás Gulácsi.
//
// SPDX-License-Identifier: LGPL-3.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/UNO-SOFT/vltee/vlup"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	if err := Main(); err != nil {
		slog.Error("Main", "error", err)
		os.Exit(1)
	}
}

func Main() error {
	FS := ff.NewFlagSet("vltee")
	flagAccountID := FS.UintLong("account-id", uint(vlup.NewAccountID("")), "accountID")
	flagProjectID := FS.UintLong("project-id", uint(vlup.NewProjectID("")), "projectID")
	flagVLURL := FS.StringLong("vlurl", "", "VictoriaLogs url")
	flagPriority := FS.IntLong("priority", 6, "priority")
	flagKVs := FS.StringListLong("kvs", "key-value pairs separated by =")
	appCmd := ff.Command{Name: "vltee", Flags: FS,
		ShortHelp: "vltee [options] <vl-url>",
		Exec: func(ctx context.Context, args []string) error {
			if len(args) < 1 {
				if *flagVLURL == "" {
					return errors.New("destURL is required")
				}
				args = append(args, *flagVLURL)
			}
			cl, err := vlup.NewClient(args[0], vlup.AccountID(*flagAccountID), vlup.ProjectID(*flagProjectID), nil)
			if err != nil {
				return err
			}
			var vars map[string]string
			if len(*flagKVs) != 0 {
				vars = make(map[string]string, len(*flagKVs))
				for _, kv := range *flagKVs {
					if k, v, ok := strings.Cut(kv, "="); ok {
						vars[k] = v
					}
				}
			}
			return uploadLines(ctx, cl, os.Stdout, os.Stdin, *flagPriority, vars)
		},
	}

	parent := FS
	FS = ff.NewFlagSet("run")
	FS.SetParent(parent)
	flagEmails := FS.StringListLong("email", "email addresses")
	flagGetenv := FS.StringLong("get-env", os.ExpandEnv(". ${BRUNO_HOME}/../.app_env"), "bash command to set up the environment")
	flagUnitName := FS.StringLong("unit-name", "", "unit name")
	runCmd := ff.Command{Name: "run", Flags: FS,
		ShortHelp: "run program and tail output on web",
		Usage:     "run [opts] <program> [program args]",
		Exec: func(ctx context.Context, args []string) error {
			cl, err := vlup.NewClient(*flagVLURL, vlup.AccountID(*flagAccountID), vlup.ProjectID(*flagProjectID), nil)
			if err != nil {
				return err
			}
			var prog string
			cmdArgs := make([]string, 0, 1+8+len(args))
			prog = "systemd-run"
			argsAreUTF8 := utf8.Valid([]byte(args[0]))
			var buf bytes.Buffer
			for _, a := range args[1:] {
				if buf.Len() != 0 {
					buf.WriteByte(' ')
				}
				s, err := syntax.Quote(a, syntax.LangBash)
				if err != nil {
					return fmt.Errorf("quote %s: %w", a, err)
				}
				buf.WriteString(s)
				argsAreUTF8 = argsAreUTF8 && utf8.Valid([]byte(a))
			}
			hsh := sha256.Sum224(buf.Bytes())
			name := *flagUnitName
			if name == "" {
				name = (base64.RawURLEncoding.EncodeToString([]byte(args[0])) +
					"-" + base64.RawURLEncoding.EncodeToString(hsh[:]))
			}
			argsB64 := base64.StdEncoding.EncodeToString(buf.Bytes())
			var setup string
			if *flagGetenv != "" {
				setup = *flagGetenv + "; "
			}
			syslogIdentifier := "vltee-" + name

			prog = "systemd-run"
			cmdArgs = append(cmdArgs,
				"--user", "--collect", "--pipe",
				"--service-type=exec", "--unit="+name)
			if setup == "" && argsAreUTF8 {
				cmdArgs = append(cmdArgs, args...)
			} else {
				prg, err := syntax.Quote(args[0], syntax.LangBash)
				if err != nil {
					return fmt.Errorf("quote %s: %w", args[0], err)
				}
				cmdArgs = append(cmdArgs,
					"/bin/bash", "-c",
					fmt.Sprintf(
						setup+"exec %s $(echo -n '%s' | base64 -d)",
						prg, argsB64))
			}

			pr, pw := io.Pipe()
			cmd := exec.CommandContext(context.Background(), prog, cmdArgs...)
			slog.Info("start", "prog", cmd.Args)
			cmd.Stdout = pw
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("start %q: %w", cmd.Args, err)
			}

			// https://bruno2-d1.hu.emea.aegon.com:8443/logs/select/vmui/?#/?query=SYSLOG_IDENTIFIER%3Abruno-wsc+-journal-&g0.range_input=54m13s156ms&g0.end_input=2026-04-14T09%3A30%3A11&g0.relative_time=none&accountID=2&projectID=111
			fmt.Printf("%s/select/vmui/?#/?SYSLOG_IDENTIFIER=%s&accountID=%d&projectID=%d&view=liveTailing\n", *flagVLURL, syslogIdentifier, *flagAccountID, *flagProjectID)

			var vars map[string]string
			if len(*flagKVs) != 0 {
				vars = make(map[string]string, len(*flagKVs)+2)
				for _, kv := range *flagKVs {
					if k, v, ok := strings.Cut(kv, "="); ok {
						vars[k] = v
					}
				}
			} else {
				vars = make(map[string]string, 2)
			}
			vars["SYSLOG_IDENTIFIER"] = syslogIdentifier
			slog.Info("logging", "priority", *flagPriority, "vars", vars)

			go func() {
				defer pw.Close()
				err := uploadLines(ctx, cl, os.Stdout, pr, *flagPriority, vars)
				pw.CloseWithError(err)
			}()

			err = cmd.Wait()
			slog.Warn("finished", "error", err)
			if len(*flagEmails) == 0 {
				return err
			}
			var typ string
			var rc int
			if err == nil {
				typ = "sikeresen"
			} else {
				typ = "HIBAval"
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					rc = ee.ExitCode()
				}
			}
			mail := exec.CommandContext(ctx, "mail",
				append(append(make([]string, 0, 2+len(*flagEmails)),
					"-s", strings.Join(args, " ")+" "+typ+" lefutott"),
					*flagEmails...)...)
			mail.Stdin = strings.NewReader(
				strings.Join(cmd.Args, " ") + ": " + strconv.Itoa(rc))
			mail.Stdout, mail.Stderr = os.Stdout, os.Stderr
			if err := mail.Start(); err != nil {
				slog.Error("sending mail", "cmd", mail.Args, "error", err)
				return nil
			}
			return mail.Wait()
		},
	}
	appCmd.Subcommands = append(appCmd.Subcommands, &runCmd)

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

func uploadLines(ctx context.Context, cl vlup.Client, w io.Writer, r io.Reader, priority int, vars map[string]string) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	if vars == nil {
		vars = map[string]string{"LINE": ""}
	}

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
				slog.Error("UploadJournal", "to", cl.URL, "data", buf.String(), "error", err)
			}
			buf.Reset()
			mu.Unlock()
			if exit {
				return
			}
		}
	})
	var line int64
	var out bytes.Buffer
	for scanner.Scan() {
		line++
		vars["LINE"] = strconv.FormatInt(line, 10)
		mu.Lock()
		err := vlup.WriteJournalEntry(&buf, priority, scanner.Bytes(), vars)
		mu.Unlock()
		if err != nil {
			slog.Error("WriteJournalEntry", "error", err)
		}
		if w != nil {
			out.Reset()
			out.Write(scanner.Bytes())
			out.WriteByte('\n')
			w.Write(out.Bytes())
		}
	}
	close(done)
	wg.Wait()
	return nil
}
