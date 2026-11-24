// SPDX-License-Identifier: AGPL-3.0-only

// Package clickhouse is a minimal client for ClickHouse's HTTP interface:
// enough to run statements, insert rows as JSON and read rows as JSON.
package clickhouse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"softswitch/core/db"
)

type Client struct {
	base   string // scheme://host:port/
	params url.Values
	user   *url.Userinfo
	http   *http.Client
}

// New takes the HTTP address of the server with the database as its path,
// such as http://user:password@host:8123/softswitch.
func New(target string) (*Client, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid ClickHouse address")
	}
	p := url.Values{
		"date_time_input_format":                  {"best_effort"},
		"input_format_skip_unknown_fields":        {"1"},
		"input_format_null_as_default":            {"1"},
		"output_format_json_quote_64bit_integers": {"0"},
	}
	if name := strings.Trim(u.Path, "/"); name != "" {
		p.Set("database", name)
	}
	return &Client{base: u.Scheme + "://" + u.Host + "/", params: p, user: u.User,
		http: &http.Client{Timeout: time.Minute}}, nil
}

// Exec runs a statement; body, if any, is the data of an insert.
func (c *Client) Exec(ctx context.Context, query string, body io.Reader) error {
	_, err := c.do(ctx, query, nil, body)
	return err
}

// Query runs a select and returns the raw answer in the format the query
// names. params fill {name:Type} placeholders in the query.
func (c *Client) Query(ctx context.Context, query string, params map[string]string) ([]byte, error) {
	return c.do(ctx, query, params, nil)
}

// QueryStream runs a select and returns its answer as it arrives, for
// answers too large to hold at once (a CSV export, say). The caller closes
// it; an error ClickHouse meets part way through ends the stream early.
func (c *Client) QueryStream(ctx context.Context, query string, params map[string]string) (io.ReadCloser, error) {
	resp, err := c.open(ctx, query, params, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) do(ctx context.Context, query string, params map[string]string, body io.Reader) ([]byte, error) {
	resp, err := c.open(ctx, query, params, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// open sends a request and returns the response once it is known to be a
// success; the body is the caller's to close.
func (c *Client) open(ctx context.Context, query string, params map[string]string, body io.Reader) (*http.Response, error) {
	p := url.Values{}
	for k, v := range c.params {
		p[k] = v
	}
	for k, v := range params {
		p.Set("param_"+k, v)
	}
	// The statement travels in the body unless the body carries data.
	if body == nil {
		body = strings.NewReader(query)
	} else {
		p.Set("query", query)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"?"+p.Encode(), body)
	if err != nil {
		return nil, err
	}
	if c.user != nil {
		pw, _ := c.user.Password()
		req.SetBasicAuth(c.user.Username(), pw)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("clickhouse: %s: %s", resp.Status, bytes.TrimSpace(out))
	}
	return resp, nil
}

// Migrate runs every schema file of db/migrations/clickhouse in order.
// Each is safe to run again, and several services do so as they start.
func Migrate(ctx context.Context, c *Client) error {
	const dir = "migrations/clickhouse"
	entries, err := fs.ReadDir(db.ClickHouse, dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		sql, err := fs.ReadFile(db.ClickHouse, dir+"/"+e.Name())
		if err != nil {
			return err
		}
		if err := c.Exec(ctx, string(sql), nil); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}
