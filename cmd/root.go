package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/teal-bauer/chatto-cli/api"
	"github.com/teal-bauer/chatto-cli/config"
)

var Version = "dev"

var (
	flagProfile  string
	flagInstance string
	flagJSON     bool
	flagDebug    bool
)

var rootCmd = &cobra.Command{
	Use:     "chatto",
	Short:   "Command-line client for Chatto",
	Long:    "chatto-cli lets you interact with a Chatto instance from the terminal.",
	Version: Version,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "config profile to use")
	rootCmd.PersistentFlags().StringVarP(&flagInstance, "instance", "i", "", "override instance URL")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output as JSON")
	rootCmd.PersistentFlags().BoolVar(&flagDebug, "debug", false, "print protobuf JSON alongside rendered output")
}

// clientFromFlags builds an API client from the global flags + config.
func clientFromFlags() (*api.Client, error) {
	prof, _, err := config.GetProfile(flagProfile, flagInstance)
	if err != nil {
		return nil, err
	}
	if prof.Token == "" && prof.Session == "" {
		return nil, fmt.Errorf("no credentials in profile; run `chatto login` first")
	}
	return api.New(prof.Instance, prof.Token, prof.Session), nil
}

// resolveRoom resolves a room ID, exact name, or "#name" against the rooms
// visible to the current user.
func resolveRoom(ctx context.Context, c *api.Client, ref string) (string, error) {
	return c.ResolveRoomID(ctx, ref)
}

// printJSON marshals v as pretty JSON to stdout. Use for plain Go values
// (config profiles etc); for protobuf messages use printProtoJSON /
// printProtoJSONList instead, since proto oneof fields don't marshal
// meaningfully via encoding/json.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// printProtoJSON marshals a single protobuf message as JSON to stdout.
func printProtoJSON(msg proto.Message) {
	b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: marshaling JSON: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// printProtoJSONList marshals a slice of protobuf messages as a JSON array.
func printProtoJSONList[T proto.Message](items []T) {
	raws := make([]json.RawMessage, len(items))
	for i, it := range items {
		b, err := protojson.Marshal(it)
		if err != nil {
			b = []byte("null")
		}
		raws[i] = b
	}
	printJSON(raws)
}

// tw returns a new tab-separated tabwriter for aligned column output.
func tw() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// bold wraps text in ANSI bold if stdout is a terminal.
func bold(s string) string {
	if !isTTY() {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

// dim wraps text in ANSI dim.
func dim(s string) string {
	if !isTTY() {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

// green wraps text in ANSI green.
func green(s string) string {
	if !isTTY() {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

// cyan wraps text in ANSI cyan.
func cyan(s string) string {
	if !isTTY() {
		return s
	}
	return "\033[36m" + s + "\033[0m"
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// truncate shortens s to maxLen chars, appending "…" if needed.
func truncate(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	r := []rune(s)
	return string(r[:maxLen-1]) + "…"
}

// stripNewlines replaces newlines with spaces for single-line display.
func stripNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
}
