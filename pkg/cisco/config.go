package cisco

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Config-mode helpers. Classic IOS lacks transactional config (no
// candidate datastore like IOS-XE's `configure replace` or NX-OS's
// `configuration rollback`); we apply lines serially, detect IOS
// error markers in the output, and capture before/after snapshots
// for audit. On mid-sequence failure the device is left partially
// configured — the handler logs the before snapshot so the admin
// can roll back manually if needed.

// iosErrorMarkers are the prefixes IOS emits when a command fails.
// We scan each config reply for these to decide "did that line
// actually apply?". Not exhaustive — vendor extensions / NX-OS
// add their own, but these cover the >90% case.
var iosErrorMarkers = []string{
	"% Invalid input detected",
	"% Incomplete command",
	"% Ambiguous command",
	"% Unknown command",
	"% Authorization failed",
	"% Access denied",
	// Leading whitespace on "Error:" catches a few NX-OS-ish variants.
	"Error:",
}

// HasIOSError returns (true, firstMatch) when `out` contains one
// of the known IOS error markers, else (false, ""). The match is
// the literal marker string — handlers can surface it verbatim.
func HasIOSError(out string) (bool, string) {
	for _, m := range iosErrorMarkers {
		if strings.Contains(out, m) {
			return true, m
		}
	}
	return false, ""
}

// trailingErrorLine pulls the first line containing an IOS error
// marker, including the caret-pointer line that sometimes follows,
// so error responses surface the relevant snippet rather than the
// whole (often-long) command output.
func trailingErrorLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, ln := range lines {
		ok, _ := HasIOSError(ln)
		if !ok {
			continue
		}
		// Include one trailing line if it looks like the caret pointer
		// (e.g. "  ^").
		end := i + 1
		if end < len(lines) && strings.TrimSpace(lines[end]) != "" &&
			(strings.Contains(lines[end], "^") || strings.HasPrefix(strings.TrimSpace(lines[end]), "%")) {
			end++
		}
		return strings.Join(lines[i:end], "\n")
	}
	return strings.TrimSpace(out)
}

// reStripPromptFollow helps clean stray prompts that leak into the
// running-config output when the remote device doesn't emit a
// clean end-of-data marker. We trim anything after the last blank
// line that looks like a bare prompt.
var reStripPromptFollow = regexp.MustCompile(`(?m)^\S+[>#]\s*$`)

// ShowRunningConfig returns the device's active configuration. When
// `section` is non-empty, it narrows via the IOS pipe filter
// (`show running-config | section <name>`) — useful for audit
// snapshots scoped to just the feature we're changing (e.g. "vlan"
// before + after a VLAN create).
//
// Returns the raw text; caller stores / logs / diffs as they like.
// Some devices format this output with line-wrap; we ran `terminal
// width 0` during Dial to suppress that.
func (c *Client) ShowRunningConfig(ctx context.Context, section string) (string, error) {
	cmd := "show running-config"
	if section != "" {
		cmd = fmt.Sprintf("show running-config | section %s", section)
	}
	out, err := c.RunCommand(ctx, cmd)
	if err != nil {
		return "", err
	}
	// Strip a trailing bare prompt line if the buffer retained one.
	out = reStripPromptFollow.ReplaceAllString(out, "")
	return strings.TrimRight(out, "\n "), nil
}

// RunConfigLines enters config mode, sends each line sequentially,
// exits config mode, and (when save=true) runs `write memory` to
// persist the change across reloads.
//
// On any per-line error (detected via HasIOSError on the reply),
// RunConfigLines sends `end` to leave config mode cleanly and
// returns the error immediately — the device is left in whatever
// partial state the preceding lines produced. Atomic rollback isn't
// available on classic IOS; the handler layer is expected to snapshot
// running-config before calling and log both snapshots.
//
// Each returned per-line "reply" is the raw output from RunCommand —
// typically empty on success (IOS is quiet on well-formed config
// changes) or the error marker + offending line on failure.
func (c *Client) RunConfigLines(ctx context.Context, lines []string, save bool) ([]string, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	// Enter config mode. "configure terminal" is the universal IOS /
	// IOS-XE / NX-OS command.
	if reply, err := c.RunCommand(ctx, "configure terminal"); err != nil {
		return nil, fmt.Errorf("enter config mode: %w", err)
	} else if ok, _ := HasIOSError(reply); ok {
		// Rare — would mean our user isn't privileged. The Dial-time
		// escalation should have caught this, but surface clearly.
		return nil, fmt.Errorf("enter config mode: %s", trailingErrorLine(reply))
	}

	replies := make([]string, 0, len(lines))
	var firstErr error
	for _, line := range lines {
		reply, err := c.RunCommand(ctx, line)
		replies = append(replies, reply)
		if err != nil {
			firstErr = fmt.Errorf("apply %q: %w", line, err)
			break
		}
		if ok, _ := HasIOSError(reply); ok {
			firstErr = fmt.Errorf("apply %q: %s", line, trailingErrorLine(reply))
			break
		}
	}

	// Exit config mode — even on error, so we leave the device at
	// the privileged prompt, not stuck in config sub-modes.
	if _, err := c.RunCommand(ctx, "end"); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("exit config mode: %w", err)
	}

	if firstErr != nil {
		return replies, firstErr
	}

	// Persist. `write memory` is the classic form; IOS-XE also
	// accepts it. NX-OS prefers `copy running-config startup-config`
	// but also tolerates `copy run start`. Use the lowest-common-
	// denominator classic form; extend per-platform if needed.
	if save {
		if reply, err := c.RunCommand(ctx, "write memory"); err != nil {
			return replies, fmt.Errorf("write memory: %w", err)
		} else if ok, _ := HasIOSError(reply); ok {
			return replies, fmt.Errorf("write memory: %s", trailingErrorLine(reply))
		}
	}
	return replies, nil
}
