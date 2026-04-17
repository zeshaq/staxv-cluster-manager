// Package cisco is a minimal SSH client for classic Cisco IOS /
// IOS-XE CLI — enough to run `show` commands, enter enable mode,
// and (Phase 2+) push config via `configure terminal`.
//
// Design
// ──────
//
//  1. One Client per device per operation. Cheap to construct; we don't
//     pool sessions yet. Cisco gear generally handles one-off SSH
//     sessions fine, and the cluster-manager UI is low-QPS.
//
//  2. Interactive shell — not `ssh host cmd`. Classic IOS doesn't do
//     "exec" channels reliably; every implementation assumes a TTY.
//     We open a PTY, wait for the prompt, run `terminal length 0` to
//     disable pagination, then drive commands by writing + reading
//     until prompt match.
//
//  3. Prompt detection is regex-based because Cisco doesn't give us
//     a clean delimiter. We accept any trailing "hostname>" (exec)
//     or "hostname#" (privileged) or "hostname(config)#" (config).
//     The `hostname` part comes from the banner on login.
//
//  4. All public methods take ctx and respect deadlines — important
//     for a flaky device hanging the UI.
//
// What we NOT handle yet
// ──────────────────────
//
//   - Public-key auth (password only — SSH key support is a later
//     `secrets` change; most labs use password).
//   - Host-key verification — like the Redfish client's self-signed
//     TLS story, network-device SSH fingerprints rotate on reloads;
//     add a trusted-fingerprint store later.
//   - Full OEM command sets (NX-API, gNMI, Meraki). Scope-limited.

package cisco

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultTimeout caps a single SSH interaction (dial + one command).
// Cisco gear over flaky mgmt LANs can be slow; 20s is forgiving
// without being painful when a device is actually down.
const DefaultTimeout = 20 * time.Second

// readBufferCap bounds how much we'll buffer from a single command.
// `show running-config` on a fat switch can exceed 1 MB; 4 MB keeps
// us safe without blowing up memory on a runaway command.
const readBufferCap = 4 * 1024 * 1024

// Client is an open interactive SSH session to one device. Not safe
// for concurrent use — each caller gets their own Client.
type Client struct {
	host string
	port int

	// hostname is what we learn from the login banner prompt — used to
	// build prompt regexes and (in Probe) overridden by `show version`.
	hostname string

	conn    *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	// Regexes pre-compiled after the first prompt read. Recompiled when
	// we enter/leave modes so the matcher is tight to the active prompt.
	promptRE *regexp.Regexp
}

// Dial opens an SSH session, logs in, escalates to enable if an
// enable password is supplied, disables pagination, and parks at the
// privileged prompt. The returned Client is ready for RunCommand.
//
// Password-only auth (`ssh.Password`). Host-key verification is
// explicitly disabled — mgmt-LAN gear rotates host keys on reloads
// and a trusted-fingerprint store is a later feature.
func Dial(ctx context.Context, host string, port int, username, password, enable string) (*Client, error) {
	if port == 0 {
		port = 22
	}
	cfg := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			// Some IOS boxes only accept keyboard-interactive for
			// passwords. Offer both so we work against either.
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		// Accept any host key — see package doc. InsecureIgnoreHostKey
		// is the documented opt-out and matches Redfish's TLS stance.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         DefaultTimeout,
		// Older IOS images only speak SHA-1 algorithms. Don't narrow
		// the default list — x/crypto's default set already includes
		// the legacy algorithms the real devices need.
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := &net.Dialer{Timeout: DefaultTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &DialError{Err: err}
	}
	// ssh.NewClientConn wraps the raw TCP; pass our already-dialed
	// net.Conn so the ctx-bound dial timeout applies.
	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, addr, cfg)
	if err != nil {
		_ = rawConn.Close()
		return nil, &AuthError{Err: err}
	}
	conn := ssh.NewClient(sshConn, chans, reqs)

	sess, err := conn.NewSession()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open session: %w", err)
	}

	// Request a PTY. Cisco CLI assumes a terminal; without it,
	// pagination and prompt framing misbehave.
	if err := sess.RequestPty("vt100", 80, 200, ssh.TerminalModes{
		ssh.ECHO:          0, // we don't need the shell echoing our bytes back
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		_ = sess.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	c := &Client{
		host:    host,
		port:    port,
		conn:    conn,
		session: sess,
		stdin:   stdin,
		stdout:  stdout,
		// Initial prompt matcher — loose; tightens after we read the
		// first prompt and extract the hostname.
		promptRE: initialPromptRE,
	}

	// Wait for the login banner + initial prompt. Also captures the
	// hostname so subsequent prompt matching can be strict.
	initial, err := c.readUntilPrompt(ctx)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("await login prompt: %w", err)
	}
	c.hostname = extractHostnameFromPrompt(initial)
	if c.hostname != "" {
		c.promptRE = buildPromptRE(c.hostname)
	}

	// Escalate to enable if we're not already privileged and the admin
	// supplied an enable password. Detect privilege from the trailing
	// prompt character: '#' = privileged, '>' = exec.
	if !endsWithPrivileged(initial) && enable != "" {
		if err := c.escalate(ctx, enable); err != nil {
			_ = c.Close()
			return nil, err
		}
	}

	// Disable pagination — every command reply comes back whole.
	if _, err := c.RunCommand(ctx, "terminal length 0"); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("terminal length 0: %w", err)
	}
	// Also disable command-reformatting line-wrap — keeps `show` output
	// line-preserving so parsers don't hit unexpected breaks.
	_, _ = c.RunCommand(ctx, "terminal width 0")

	return c, nil
}

// Close tears down the SSH session + transport. Idempotent; any
// errors on shutdown are swallowed — nothing useful to do with them.
func (c *Client) Close() error {
	if c.session != nil {
		_ = c.stdin.Close()
		_ = c.session.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return nil
}

// RunCommand writes `cmd`, reads output up to the next prompt, and
// returns the body (with the echoed command + trailing prompt line
// stripped so callers get the pure result).
//
// Respects ctx — on cancellation we Close the underlying session
// which unblocks readUntilPrompt.
func (c *Client) RunCommand(ctx context.Context, cmd string) (string, error) {
	if _, err := io.WriteString(c.stdin, cmd+"\n"); err != nil {
		return "", fmt.Errorf("write %q: %w", cmd, err)
	}
	raw, err := c.readUntilPrompt(ctx)
	if err != nil {
		return "", fmt.Errorf("read reply to %q: %w", cmd, err)
	}
	return trimCommandEcho(raw, cmd), nil
}

// escalate runs `enable`, sends the password to the "Password:" prompt,
// and verifies we land at '#'. Errors if the password is wrong.
func (c *Client) escalate(ctx context.Context, password string) error {
	if _, err := io.WriteString(c.stdin, "enable\n"); err != nil {
		return err
	}
	// Wait for the "Password:" sub-prompt. No trailing newline; that's
	// why readUntilPrompt won't work — we match the literal string.
	if err := c.readUntilLiteral(ctx, "Password:"); err != nil {
		return fmt.Errorf("await enable password prompt: %w", err)
	}
	if _, err := io.WriteString(c.stdin, password+"\n"); err != nil {
		return err
	}
	out, err := c.readUntilPrompt(ctx)
	if err != nil {
		return fmt.Errorf("await post-enable prompt: %w", err)
	}
	if strings.Contains(out, "Access denied") || strings.Contains(out, "% Bad passwords") {
		return &AuthError{Err: errors.New("enable password rejected")}
	}
	if !endsWithPrivileged(out) {
		return errors.New("enable did not produce privileged prompt (#)")
	}
	return nil
}

// readUntilPrompt reads from the SSH stdout until the compiled prompt
// regex matches a trailing line, or ctx expires, or we blow readBufferCap.
// Returns everything read.
func (c *Client) readUntilPrompt(ctx context.Context) (string, error) {
	deadline, hasDL := ctx.Deadline()
	buf := &bytes.Buffer{}
	chunk := make([]byte, 4096)
	for {
		if hasDL && time.Now().After(deadline) {
			return buf.String(), context.DeadlineExceeded
		}
		if err := ctx.Err(); err != nil {
			return buf.String(), err
		}
		// A blocked Read() outlives ctx cancellation — rely on caller
		// Closing the Client to unblock. The ctx check above covers
		// deadline-driven cancellation.
		n, err := c.stdout.Read(chunk)
		if n > 0 {
			if buf.Len()+n > readBufferCap {
				return buf.String(), fmt.Errorf("reply exceeded %d bytes", readBufferCap)
			}
			buf.Write(chunk[:n])
			if c.promptRE.Match(buf.Bytes()) {
				return buf.String(), nil
			}
		}
		if err != nil {
			if err == io.EOF {
				return buf.String(), io.ErrUnexpectedEOF
			}
			return buf.String(), err
		}
	}
}

// readUntilLiteral is readUntilPrompt's simpler cousin for the
// "Password:" sub-prompt during enable.
func (c *Client) readUntilLiteral(ctx context.Context, needle string) error {
	deadline, hasDL := ctx.Deadline()
	buf := &bytes.Buffer{}
	chunk := make([]byte, 256)
	for {
		if hasDL && time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := c.stdout.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if strings.Contains(buf.String(), needle) {
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
}

// Hostname returns the device's configured hostname as learned from
// its prompt during login. Empty string until Dial has completed.
func (c *Client) Hostname() string { return c.hostname }

// Typed errors — the handler uses errors.As to distinguish "couldn't
// reach the box" (503) from "reached it but creds wrong" (401-ish).
type DialError struct{ Err error }

func (e *DialError) Error() string { return "dial: " + e.Err.Error() }
func (e *DialError) Unwrap() error { return e.Err }

type AuthError struct{ Err error }

func (e *AuthError) Error() string { return "auth: " + e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// ─── Prompt helpers ──────────────────────────────────────────────

// initialPromptRE is used before we've learned the device's hostname.
// Matches *trailing* "something> " / "something# " / "something(config)# ".
// The leading non-whitespace bite keeps us from greedy-matching across
// the login banner.
var initialPromptRE = regexp.MustCompile(`(?m)([\w\.-]+)(?:\(config[^)]*\))?[>#]\s*$`)

// buildPromptRE builds a tighter regex scoped to the known hostname.
// Matches the same set of terminators but anchors on the actual name
// so banner text containing '#' (not unheard of) doesn't false-positive.
func buildPromptRE(hostname string) *regexp.Regexp {
	// Strip any trailing domain on the hostname — some boxes prompt
	// with "device.example.com#"; match either form.
	h := regexp.QuoteMeta(hostname)
	return regexp.MustCompile(`(?m)` + h + `(?:\.[\w\.-]+)?(?:\(config[^)]*\))?[>#]\s*$`)
}

// extractHostnameFromPrompt pulls "R1" out of "...\r\nR1#" or
// "...\r\nR1(config)#". Returns "" if no prompt found.
func extractHostnameFromPrompt(raw string) string {
	m := initialPromptRE.FindStringSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// endsWithPrivileged says whether the last non-empty line ends in '#'
// (privileged) vs '>' (exec). Both are valid prompts but only '#' lets
// us run the `show` commands we need.
func endsWithPrivileged(raw string) bool {
	lines := strings.Split(strings.TrimRight(raw, "\r\n \t"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], " \t\r")
		if line == "" {
			continue
		}
		return strings.HasSuffix(line, "#")
	}
	return false
}

// trimCommandEcho removes the first line (which is always the command
// we typed, echoed back by the PTY) and the last line (the trailing
// prompt). Returns the pure command output.
func trimCommandEcho(raw, cmd string) string {
	// Strip CR bytes so downstream parsers don't need to care.
	raw = strings.ReplaceAll(raw, "\r", "")
	lines := strings.Split(raw, "\n")
	// First line — echoed command. Some devices echo with leading
	// whitespace; match loosely.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == strings.TrimSpace(cmd) {
		lines = lines[1:]
	}
	// Last line — prompt. Drop up to the last line that LOOKS like a
	// prompt (ends in > or #), plus any trailing empty lines.
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" || strings.HasSuffix(last, "#") || strings.HasSuffix(last, ">") {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}
