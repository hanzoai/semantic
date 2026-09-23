package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/hanzoai/semantic"
)

// Web fetches one HTTP or HTTPS URL.
//
// By default it refuses to reach an internal address — loopback, private,
// link-local, carrier NAT, cloud metadata — because a URL is usually
// attacker-influenced and the machine doing the fetching can see more of the
// network than the person asking for it. The check runs before the request
// and again on the socket the connection actually lands on, so a hostname
// that resolves to a public address and then to 127.0.0.1 still fails. Set
// Private to turn it off for a trusted internal deployment.
//
// The clients this package builds connect directly and never through a proxy
// named in the environment: through a proxy, the socket the rules check is the
// proxy's, and the target's address is never seen. A host that fetches through
// its own egress supplies Client.
type Web struct {
	// Client sends the request, and is where a host injects its own transport:
	// an egress proxy, its own dialer, its own address rules. It is used as
	// given — its proxy, dialing and redirect policy are the caller's — and
	// Web still applies what does not depend on the transport: the scheme and
	// pre-flight address check (unless Private), Timeout, and Max.
	//
	// Nil uses a shared client that connects directly and enforces the
	// address rules on every connection, redirects included.
	Client *http.Client
	// Agent is the User-Agent header, "semantic" when empty.
	Agent string
	// Header carries additional request headers.
	Header http.Header
	// Timeout bounds the whole fetch, 30s when zero.
	Timeout time.Duration
	// Max is the largest response body to read, in bytes. Zero means 32 MiB.
	// There is no unbounded setting: how much a response holds is the
	// server's choice, and a caller that expects more says how much.
	Max int64
	// Private allows internal addresses.
	Private bool
	// Decode turns the body into documents. Nil picks a decoder from the
	// response content type, falling back to the URL's extension.
	Decode Decoder
}

// Ingest fetches ref and decodes the response body.
func (w Web) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, ErrEmpty
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", ref, err)
	}
	if !w.Private {
		if err := safe(u); err != nil {
			return nil, err
		}
		if err := reach(ctx, u.Hostname()); err != nil {
			return nil, err
		}
	}

	wait := w.Timeout
	if wait <= 0 {
		wait = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", ref, err)
	}
	for k, vs := range w.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	agent := w.Agent
	if agent == "" {
		agent = "semantic"
	}
	req.Header.Set("User-Agent", agent)

	resp, err := w.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ingest: fetch %s: %w", ref, err)
	}
	defer resp.Body.Close()

	limit := w.Max
	if limit <= 0 {
		limit = most
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ingest: read %s: %w", ref, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("ingest: %s over %d bytes: %w", ref, limit, ErrSize)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ingest: %s: %s", ref, resp.Status)
	}

	final := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	ct := resp.Header.Get("Content-Type")
	o := Origin{
		Ref:    final,
		Size:   int64(len(b)),
		Hash:   sum(b),
		Type:   guess(ct, u.Path),
		Mime:   bare(ct),
		Status: resp.StatusCode,
		At:     time.Now(),
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		o.Mod = t
	}
	return decode(w.Decode, o).Decode(b, o)
}

func (w Web) client() *http.Client {
	switch {
	case w.Client != nil:
		return w.Client
	case w.Private:
		return open
	}
	return guard
}

// most is the response size Web reads when Max is unset.
const most = 32 << 20

// open fetches without address restrictions, for Private.
var open = &http.Client{Transport: direct(nil)}

// guard refuses to connect to an internal address, whichever hostname
// resolves to it and however many redirects lead there.
var guard = &http.Client{
	Transport: direct(func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || internal(ip) {
			return fmt.Errorf("%w: %s", ErrBlocked, address)
		}
		return nil
	}),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("ingest: too many redirects")
		}
		return safe(req.URL)
	},
}

// direct is a transport that dials the target itself, with no proxy, and
// runs control on every socket before it connects.
func direct(control func(network, address string, c syscall.RawConn) error) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   control,
		}).DialContext,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// safe rejects a URL this package must not fetch: a scheme other than http
// or https, a missing host, a loopback name, or a literal internal address.
// It does no name resolution.
func safe(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("ingest: scheme %q is not fetchable: %w", u.Scheme, ErrBlocked)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("ingest: %s has no host: %w", u, ErrBlocked)
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return fmt.Errorf("ingest: %s: %w", host, ErrBlocked)
	}
	if ip := net.ParseIP(host); ip != nil && internal(ip) {
		return fmt.Errorf("ingest: %s: %w", host, ErrBlocked)
	}
	return nil
}

// reach resolves host and rejects it if any address it answers with is
// internal. A name that cannot be resolved is rejected too: the request must
// not proceed on an unconfirmed address.
func reach(ctx context.Context, host string) error {
	if net.ParseIP(host) != nil {
		return nil // already checked as a literal
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("ingest: %s did not resolve: %w", host, err)
	}
	for _, a := range ips {
		if internal(a.IP) {
			return fmt.Errorf("ingest: %s resolves to %s: %w", host, a.IP, ErrBlocked)
		}
	}
	return nil
}

// internal reports whether ip belongs to an address space a fetch must not
// reach: loopback, private, link-local (which includes cloud metadata at
// 169.254.169.254), multicast, unspecified, carrier NAT and reserved space.
func internal(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, n := range shut {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// shut is the address space Go's own predicates do not cover: carrier-grade
// NAT, "this network", and reserved space.
var shut = nets("100.64.0.0/10", "0.0.0.0/8", "240.0.0.0/4", "::/128")

func nets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// guess names the body's type from the response content type, falling back to
// the URL's extension.
func guess(ct, urlPath string) string {
	switch strings.ToLower(bare(ct)) {
	case "application/json", "text/json":
		return "json"
	case "application/x-ndjson", "application/jsonl", "application/x-jsonlines":
		return "jsonl"
	case "text/csv":
		return "csv"
	case "text/tab-separated-values":
		return "tsv"
	case "text/html", "application/xhtml+xml":
		return "html"
	}
	if ext := strings.ToLower(strings.TrimPrefix(path.Ext(urlPath), ".")); ext != "" {
		return ext
	}
	return "unknown"
}

// bare is the media type of a Content-Type header, without parameters.
func bare(ct string) string {
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.TrimSpace(strings.Split(ct, ";")[0])
	}
	return t
}
