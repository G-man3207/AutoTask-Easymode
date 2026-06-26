package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02T15:04:05"
	// dateTimeZoned carries an explicit zone so Autotask records the correct
	// instant. Emitting naive local times made Autotask read them as UTC and
	// shift the displayed window by the local offset.
	dateTimeZoned  = "2006-01-02T15:04:05Z07:00"
	requestTimeout = 60 * time.Second
)

// newFlagSet builds a FlagSet that never writes to stderr; parse errors are
// surfaced through the JSON Result instead.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// usageErr wraps a flag parse error with a usage hint.
func usageErr(cmd string, err error) error {
	return hinted("run `atem help` for usage", "%s: %v", cmd, err)
}

// cmdContext returns a context bounded by the standard request timeout.
func cmdContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), requestTimeout)
}

// defOr returns v unless it is zero, in which case it returns fallback.
func defOr(v, fallback int) int {
	if v != 0 {
		return v
	}
	return fallback
}

// firstArg returns the first element of args, or "".
func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// splitLeadingArgs separates leading positional arguments (those before the
// first flag) from the remaining flag arguments. Go's flag package stops at
// the first non-flag token, so commands that take a positional id followed by
// flags (e.g. `timer stop s1 --close`) must split before parsing.
func splitLeadingArgs(args []string) (positionals, rest []string) {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	return args[:i], args[i:]
}

// pickArg returns the first positional, falling back to the first trailing
// parsed arg, so both "id --flags" and "--flags id" orderings work.
func pickArg(positionals, parsed []string) string {
	if s := firstArg(positionals); s != "" {
		return s
	}
	return firstArg(parsed)
}

const defaultSearchLimit = 25

// searchArgs is the parsed form of a free-text search command line.
type searchArgs struct {
	query   string
	limit   int
	company string
}

// asFlag classifies a token: ("limit","5",true) for --limit=5, ("limit","",true)
// for --limit, and ("","",false) for a plain word.
func asFlag(tok string) (name, value string, isFlag bool) {
	if len(tok) < 2 || !strings.HasPrefix(tok, "-") || tok == "--" {
		return "", "", false
	}
	s := strings.TrimLeft(tok, "-")
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", true
}

// parseSearch parses a free-text search command line where flags (--limit,
// --company) may appear before OR after the query words. Go's flag package
// stops at the first non-flag token, which silently swallowed flags placed after
// the query — this parser is deliberately position-independent.
func parseSearch(args []string) (searchArgs, error) {
	out := searchArgs{limit: defaultSearchLimit}
	var words []string
	for i := 0; i < len(args); i++ {
		name, val, isFlag := asFlag(args[i])
		if !isFlag {
			words = append(words, args[i])
			continue
		}
		if !strings.Contains(args[i], "=") {
			if i+1 >= len(args) {
				return out, hinted("provide a value", "flag -%s needs a value", name)
			}
			i++
			val = args[i]
		}
		switch name {
		case "limit":
			n, err := strconv.Atoi(val)
			if err != nil {
				return out, hinted("--limit must be an integer", "invalid --limit %q", val)
			}
			out.limit = n
		case "company":
			out.company = val
		default:
			return out, hinted("supported flags: --limit, --company", "unknown flag -%s", name)
		}
	}
	out.query = strings.TrimSpace(strings.Join(words, " "))
	return out, nil
}

// round2 rounds to two decimal places.
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// setInt parses val into dst, returning a hinted error on failure.
func setInt(dst *int, val string) error {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return hinted("value must be an integer", "invalid integer %q", val)
	}
	*dst = n
	return nil
}

// redact masks a secret for display.
func redact(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "***set***"
}

// asString coerces a decoded JSON value to a string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// asInt64 coerces a decoded JSON value to int64.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

// asFloat coerces a decoded JSON value to float64.
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

// asBool coerces common Autotask bool encodings to a typed bool.
func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "y":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
