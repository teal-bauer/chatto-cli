package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/teal-bauer/chatto-cli/api"
	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
)

// refCache resolves room-scoped event IDs to their Message, for rendering
// thread/reply context (quoting the original message). It fetches on a
// cache miss via GetMessage and remembers both hits and misses (e.g. a
// message retracted since) for the life of one command/session.
type refCache struct {
	client *api.Client

	mu    sync.Mutex
	cache map[string]*apiv1.Message
	done  map[string]bool
}

func newRefCache(client *api.Client) *refCache {
	return &refCache{client: client, cache: make(map[string]*apiv1.Message), done: make(map[string]bool)}
}

func (c *refCache) store(roomID string, msg *apiv1.Message) {
	if msg == nil || msg.GetId() == "" {
		return
	}
	key := roomID + "|" + msg.GetId()
	c.mu.Lock()
	c.cache[key] = msg
	c.done[key] = true
	c.mu.Unlock()
}

// lookup resolves eventID (within roomID) to its Message, fetching on a
// cache miss. Returns nil if eventID is empty or doesn't resolve.
func (c *refCache) lookup(ctx context.Context, roomID, eventID string) *apiv1.Message {
	if eventID == "" {
		return nil
	}
	key := roomID + "|" + eventID

	c.mu.Lock()
	if c.done[key] {
		msg := c.cache[key]
		c.mu.Unlock()
		return msg
	}
	c.mu.Unlock()

	msg, err := c.client.GetMessage(ctx, roomID, eventID)
	if err != nil {
		msg = nil
	}
	c.mu.Lock()
	c.cache[key] = msg
	c.done[key] = true
	c.mu.Unlock()
	return msg
}

// eventRenderer renders room timeline events and hydrated realtime events in
// an IRC-like format, resolving actor names and thread/reply context on
// demand.
type eventRenderer struct {
	ctx      context.Context
	client   *api.Client
	instance string
	users    *api.UserCache
	refs     *refCache
	rooms    map[string]string // room ID -> display label; nil omits room labels
}

func newEventRenderer(ctx context.Context, client *api.Client, rooms map[string]string) *eventRenderer {
	return &eventRenderer{
		ctx:      ctx,
		client:   client,
		instance: client.Instance(),
		users:    api.NewUserCache(client),
		refs:     newRefCache(client),
		rooms:    rooms,
	}
}

func (r *eventRenderer) actorName(userID string) string {
	if userID == "" {
		return "unknown"
	}
	u, err := r.users.Get(r.ctx, userID)
	if err != nil || u == nil {
		return userID
	}
	return displayName(u, userID)
}

// displayName picks the best human-readable name for u, falling back to
// fallback (typically the user ID) if u is nil or has neither name set.
func displayName(u *apiv1.User, fallback string) string {
	if u == nil {
		return fallback
	}
	if u.GetDisplayName() != "" {
		return u.GetDisplayName()
	}
	if u.GetLogin() != "" {
		return u.GetLogin()
	}
	return fallback
}

func (r *eventRenderer) roomLabel(roomID string) string {
	if r.rooms == nil {
		return ""
	}
	if name, ok := r.rooms[roomID]; ok && name != "" {
		return name
	}
	return roomID
}

// formatRef renders a short "actor: quoted body" reference to msg, used for
// reply/reaction context lines.
func (r *eventRenderer) formatRef(msg *apiv1.Message) string {
	if msg == nil {
		return ""
	}
	actor := r.actorName(msg.GetActorId())
	if msg.GetBody() == "" {
		return actor + ": [attachment]"
	}
	return actor + ": \"" + truncate(stripNewlines(expandMessageTimestamps(msg.GetBody())), 60) + "\""
}

// renderTimelineEvent renders one event from a RoomTimelinePage (as returned
// by GetRoomEvents).
func (r *eventRenderer) renderTimelineEvent(roomID string, ev *apiv1.RoomTimelineEvent) {
	ts := formatTimestamp(ev.GetCreatedAt())
	actor := r.actorName(ev.GetActorId())

	switch {
	case ev.GetMessagePosted() != nil:
		r.renderMessage(roomID, ts, ev.GetActorId(), ev.GetMessagePosted().GetMessage())
	case ev.GetRoomCreated() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** room created")
	case ev.GetRoomUpdated() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** room updated")
	case ev.GetRoomDeleted() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** room deleted")
	case ev.GetRoomArchived() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** room archived")
	case ev.GetRoomUnarchived() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** room unarchived")
	case ev.GetUserJoinedRoom() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** "+actor+" has joined")
	case ev.GetUserLeftRoom() != nil:
		printStatus(ts, r.roomLabel(roomID), "*** "+actor+" has left")
	}

	if flagDebug {
		printProtoJSON(ev)
	}
}

// renderMessage renders a Message: body, attachments, thread/reply context,
// and reactions.
func (r *eventRenderer) renderMessage(roomID, ts, actorID string, msg *apiv1.Message) {
	if msg == nil {
		return
	}
	actor := r.actorName(actorID)
	body := renderBody(msg, r.instance)

	thread := ""
	if trID := msg.GetThreadRootEventId(); trID != "" {
		thread = "thread"
		if orig := r.refs.lookup(r.ctx, roomID, trID); orig != nil && orig.GetBody() != "" {
			thread = "\"" + truncate(stripNewlines(orig.GetBody()), 40) + "\""
		}
	}

	var replyCtx []string
	if replyID := msg.GetInReplyTo(); replyID != "" {
		if orig := r.refs.lookup(r.ctx, roomID, replyID); orig != nil {
			replyCtx = append(replyCtx, "↩ "+r.formatRef(orig))
		}
	}

	var reactions []string
	for _, rx := range msg.GetReactions() {
		reactions = append(reactions, rx.GetEmoji()+" "+strconv.Itoa(int(rx.GetCount())))
	}

	r.refs.store(roomID, msg)
	printMsg(ts, r.roomLabel(roomID), thread, "<"+actor+">", body, replyCtx, reactions)
}

// renderBody builds the display string for a Message: body text plus
// attachment references.
func renderBody(msg *apiv1.Message, instance string) string {
	body := expandMessageTimestamps(msg.GetBody())
	for _, a := range msg.GetAttachments() {
		if body != "" {
			body += "\n"
		}
		body += renderAttachment(a, instance)
	}
	return body
}

// renderAttachment returns a Markdown image reference for image attachments,
// or a resolved URL for other file types. Relative URLs are prefixed with
// instance. For video attachments with completed processing, uses the first
// transcoded variant's URL.
func renderAttachment(a *apiv1.MessageAttachment, instance string) string {
	resolve := func(u string) string {
		if strings.HasPrefix(u, "/") {
			return strings.TrimRight(instance, "/") + u
		}
		return u
	}

	if vp := a.GetVideoProcessing(); vp != nil && vp.GetStatus() == apiv1.MessageVideoProcessingStatus_MESSAGE_VIDEO_PROCESSING_STATUS_COMPLETED {
		if variants := vp.GetVariants(); len(variants) > 0 {
			return resolve(variants[0].GetAssetUrl().GetUrl())
		}
	}

	url := resolve(a.GetAssetUrl().GetUrl())
	if strings.HasPrefix(a.GetContentType(), "image/") {
		return "![" + a.GetFilename() + "](" + url + ")"
	}
	return url
}

// printMsg prints a message line with proper multi-line continuation indent.
// tsRaw is the bracketed timestamp, roomName is the room display label already
// including any "#" prefix for channels (empty to omit), thread is the thread
// label (empty for non-thread messages), nick is the plain nick. body may
// contain newlines. context lines are printed after the body, dimmed, then
// reactions.
func printMsg(tsRaw, roomName, thread, nick, body string, context, reactions []string) {
	roomPart := ""
	roomVW := 0
	if roomName != "" {
		roomPart = " " + dim("["+roomName+"]")
		roomVW = 1 + 1 + len(roomName) + 1 // " [" + name + "]"
	}
	threadPart := ""
	threadVW := 0
	if thread != "" {
		label := truncate(thread, 40)
		threadPart = " " + dim("[Thread "+label+"]")
		threadVW = 1 + 8 + len(label) + 1 // " [Thread " + label + "]"
	}
	prefixVW := len(tsRaw) + roomVW + threadVW + 1 + len(nick) // ts + room + thread + " " + nick
	indent := strings.Repeat(" ", prefixVW+1)                  // +1 for the space before body

	prefix := dim(tsRaw) + roomPart + threadPart + " " + bold(nick)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	fmt.Printf("%s %s\n", prefix, lines[0])
	for _, line := range lines[1:] {
		fmt.Printf("%s%s\n", indent, line)
	}
	for _, c := range context {
		fmt.Printf("%s%s\n", indent, dim(c))
	}
	if len(reactions) > 0 {
		fmt.Printf("%s%s\n", indent, dim(strings.Join(reactions, "  ")))
	}
}

// printStatus prints a server/status message (joins, leaves, etc.).
func printStatus(tsRaw, roomName, msg string) {
	roomPart := ""
	if roomName != "" {
		roomPart = " " + dim("["+roomName+"]")
	}
	fmt.Printf("%s%s %s\n", dim(tsRaw), roomPart, dim(msg))
}

// formatTimestamp renders ts as a bracketed "[15:04]" for today or
// "[2006-01-02 15:04]" otherwise.
func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return "[]"
	}
	t := ts.AsTime()
	now := time.Now()
	if t.Year() == now.Year() && t.Month() == now.Month() && t.Day() == now.Day() {
		return "[" + t.Format("15:04") + "]"
	}
	return "[" + t.Format("2006-01-02 15:04") + "]"
}

var (
	messagesLimit  int
	messagesBefore string
	messagesAfter  string
)

var messagesCmd = &cobra.Command{
	Use:   "messages <room>",
	Short: "Show recent messages in a room",
	Args:  cobra.ExactArgs(1),
	RunE:  runMessages,
}

func init() {
	messagesCmd.Flags().IntVarP(&messagesLimit, "limit", "n", 20, "number of messages to fetch")
	messagesCmd.Flags().StringVar(&messagesBefore, "before", "", "opaque cursor: fetch the page before this one (see RoomTimelinePage.start_cursor)")
	messagesCmd.Flags().StringVar(&messagesAfter, "after", "", "opaque cursor: fetch the page after this one (see RoomTimelinePage.end_cursor)")
	rootCmd.AddCommand(messagesCmd)
}

func runMessages(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	roomID, err := resolveRoom(ctx, c, args[0])
	if err != nil {
		return err
	}

	page, err := c.GetRoomEvents(ctx, roomID, int32(messagesLimit), messagesBefore, messagesAfter)
	if err != nil {
		return err
	}

	if flagJSON {
		printProtoJSON(page)
		return nil
	}

	renderer := newEventRenderer(ctx, c, nil)
	for _, ev := range page.GetEvents() {
		renderer.renderTimelineEvent(roomID, ev)
	}
	return nil
}

var sendCmd = &cobra.Command{
	Use:   "send <room> <message...>",
	Short: "Send a message to a room",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runSend,
}

func init() {
	rootCmd.AddCommand(sendCmd)
}

func runSend(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	roomID, err := resolveRoom(ctx, c, args[0])
	if err != nil {
		return err
	}

	body := strings.Join(args[1:], " ")

	msg, err := c.CreateMessage(ctx, roomID, body, "", "")
	if err != nil {
		return err
	}

	if flagJSON {
		printProtoJSON(msg)
		return nil
	}

	fmt.Printf("Sent (event %s)\n", msg.GetId())
	return nil
}
