// Copyright 2026 Tamás Gulácsi.
//
// SPDX-License-Identifier: LGPL-3.0

package vlup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func NewClient(vlogURL string, accountID AccountID, projectID ProjectID, client *http.Client) (Client, error) {
	if client == nil {
		client = http.DefaultClient
	}
	cl := Client{accountID: accountID.String(), projectID: projectID.String(), client: client}

	var err error
	cl.URL, err = url.JoinPath(vlogURL, "/insert/journald/upload")
	return cl, err
}

func (cl Client) UploadJournal(ctx context.Context, b []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", cl.URL, bytes.NewReader(b))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/vnd.fdo.journal")
	req.Header.Set("VL-Stream-Fields", "SYSLOG_IDENTIFIER")
	req.Header.Set("Content-Length", strconv.Itoa(len(b)))
	req.Header.Set("AccountID", cl.accountID)
	req.Header.Set("ProjectID", cl.projectID)
	resp, err := cl.client.Do(req)
	if err != nil {
		return err
	} else if resp.StatusCode >= 400 {
		return errors.New(resp.Status)
	}
	return nil
}

type (
	Client struct {
		accountID, projectID string
		URL                  string
		client               *http.Client
	}
	AccountID uint32
	ProjectID uint32
)

func (a AccountID) String() string { return strconv.FormatUint(uint64(a), 10) }
func (p ProjectID) String() string { return strconv.FormatUint(uint64(p), 10) }

/*
ACCOUNT_ID=2
if [[ "$br_prefix" = br3 ]]; then

	ACCOUNT_ID=3

fi
PROJECT_ID=1000
case "$br_env" in

	pr) PROJECT_ID=0 ;;

	dv) PROJECT_ID=1 ;;
	ts) PROJECT_ID=2 ;;
	bd) PROJECT_ID=3 ;;
	sy) PROJECT_ID=4 ;;
	df) PROJECT_ID=5 ;;

	d1) PROJECT_ID=101 ;;
	d2) PROJECT_ID=102 ;;
	pp) PROJECT_ID=110 ;;
	t1) PROJECT_ID=111 ;;
	t2) PROJECT_ID=112 ;;
	rp) PROJECT_ID=200 ;;
	*) echo "unknown PROJECT_ID ${br_env}" >&2; exit 1;;

esac
*/

func NewProjectID(s string) ProjectID {
	if s != "" {
		if u, err := strconv.ParseUint(s, 10, 32); err == nil {
			return ProjectID(u)
		} else if os.Getenv("BRUNO_ENV") == "" {
			slog.Error("NewProjectID", "string", s, "error", err)
		}
	}
	return NewBrunoProjectID(s)
}

// NewBrunoProjectID tries to guess the projectID from the file's path or the BRUNO_ENV.
func NewBrunoProjectID(s string) ProjectID {
	if s == "" {
		// fmt.Println("s0:", s)
		s = os.Getenv("BRUNO_ENV")
	} else if _, post, ok := strings.Cut(strings.TrimPrefix(s, "/bruno"), "/br"); ok {
		// fmt.Println("s1:", s, "post:", post)
		_, s, _ = strings.Cut(post, "/")
		s, _, _ = strings.Cut(s, "/")
	}
	// fmt.Println("s2:", s)
	switch s {
	case "pr", "prd", "prod":
		return 0

	case "dv", "dev":
		return 1
	case "ts", "tst":
		return 2
	case "bd", "bld":
		return 3
	case "sy", "syn":
		return 4
	case "df", "dfx":
		return 5

	case "d1", "dev1":
		return 101
	case "d2", "dev2":
		return 102
	case "pp", "prpr", "preprod":
		return 110
	case "t1", "bpt1":
		return 111
	case "t2", "bpt2":
		return 112
	case "rp", "prrp", "prodriporting":
		return 200

	default:
		return 0
	}
}

// NewAccountID tries to guess the account ID from the file's path or the BRUNO_PREFIX.
func NewAccountID(s string) AccountID {
	if s != "" {
		if u, err := strconv.ParseUint(s, 10, 32); err == nil {
			return AccountID(u)
		} else if os.Getenv("BRUNO_ENV") == "" {
			slog.Error("NewAccountID", "string", s, "error", err)
		}
	}
	return NewBrunoAccountID(s)
}

// NewBrunoAccountID tries to guess the account ID from the file's path or the BRUNO_PREFIX.
func NewBrunoAccountID(s string) AccountID {
	if s == "" {
		s = os.Getenv("BRUNO_PREFIX")
	} else if _, post, ok := strings.Cut(s, "/br"); ok {
		s = post
	} else {
		s = strings.TrimPrefix(s, "br")
	}
	for len(s) != 0 && !('0' <= s[0] && s[0] <= '9') {
		s = s[1:]
	}
	if s == "" {
		return 0
	}
	u, err := strconv.ParseUint(s[:1], 10, 32)
	if err != nil {
		slog.Error("accountID", "s", s, "parse", err)
	}
	return AccountID(u)
}
