package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/teal-bauer/chatto-cli/api"
)

// EventCache stores message content by event ID and fetches on cache miss.
type EventCache struct {
	mu       sync.Mutex
	entries  map[string]cachedMsg   // eventID -> cachedMsg
	byBodyID map[string]cachedEvent // messageBodyID -> cachedEvent
	client   *api.Client
}

type cachedMsg struct {
	actor string
	body  string
}

type cachedEvent struct {
	eventID string
	roomID  string
}

func NewEventCache(client *api.Client) *EventCache {
	return &EventCache{
		entries:  make(map[string]cachedMsg),
		byBodyID: make(map[string]cachedEvent),
		client:   client,
	}
}

func (c *EventCache) get(id string) (cachedMsg, bool) {
	if c == nil || id == "" {
		return cachedMsg{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.entries[id]
	return m, ok
}

func (c *EventCache) store(id, actor, body string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = cachedMsg{actor: actor, body: body}
}

func (c *EventCache) storeBodyID(bodyID, eventID, roomID string) {
	if c == nil || bodyID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byBodyID[bodyID] = cachedEvent{eventID: eventID, roomID: roomID}
}

// StoreEvents populates the cache from a slice of events.
func (c *EventCache) StoreEvents(events []api.SpaceEvent) {
	for _, ev := range events {
		if ev.Event.TypeName == "MessagePostedEvent" {
			c.store(ev.ID, actorName(ev), ev.Event.Body)
			if ev.Event.MessageBodyID != "" {
				c.storeBodyID(ev.Event.MessageBodyID, ev.ID, ev.Event.RoomID)
			}
		}
	}
}

// RefetchByBodyID re-fetches a message event using its messageBodyID.
func (c *EventCache) RefetchByBodyID(spaceID, bodyID string) (*api.SpaceEvent, bool) {
	if c == nil || bodyID == "" {
		return nil, false
	}
	c.mu.Lock()
	ce, ok := c.byBodyID[bodyID]
	c.mu.Unlock()
	if !ok || c.client == nil {
		return nil, false
	}
	ev, err := c.client.GetEventByID(spaceID, ce.roomID, ce.eventID)
	if err != nil || ev == nil {
		return nil, false
	}
	return ev, true
}

// Lookup returns the cached message content for eventID, fetching it by ID on
// a cache miss.
func (c *EventCache) Lookup(spaceID, roomID, eventID string) (cachedMsg, bool) {
	if c == nil || eventID == "" {
		return cachedMsg{}, false
	}
	if msg, ok := c.get(eventID); ok {
		return msg, true
	}
	if c.client == nil || roomID == "" {
		return cachedMsg{}, false
	}
	ev, err := c.client.GetEventByID(spaceID, roomID, eventID)
	if err != nil || ev == nil {
		return cachedMsg{}, false
	}
	actor := ""
	if ev.Actor != nil {
		actor = ev.Actor.DisplayName
		if actor == "" {
			actor = ev.Actor.Login
		}
	}
	c.store(ev.ID, actor, ev.Event.Body)
	return c.get(ev.ID)
}

var messagesLimit int
var messagesSince string

var messagesCmd = &cobra.Command{
	Use:   "messages <space> <room>",
	Short: "Show recent messages in a room",
	Args:  cobra.ExactArgs(2),
	RunE:  runMessages,
}

func init() {
	messagesCmd.Flags().IntVarP(&messagesLimit, "limit", "n", 20, "number of messages to fetch")
	messagesCmd.Flags().StringVar(&messagesSince, "since", "", "show messages after this event ID")
	rootCmd.AddCommand(messagesCmd)
}

func runMessages(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	spaceID, err := resolveSpace(c, args[0])
	if err != nil {
		return err
	}
	roomID, err := resolveRoom(c, spaceID, args[1])
	if err != nil {
		return err
	}

	limit := messagesLimit
	if messagesSince != "" {
		limit = 200 // fetch more so we can filter
	}
	events, err := c.GetRoomEvents(spaceID, roomID, limit)
	if err != nil {
		return err
	}

	if messagesSince != "" {
		events = eventsAfter(events, messagesSince)
	}

	if flagJSON {
		printJSON(events)
		return nil
	}

	cache := NewEventCache(c)
	cache.StoreEvents(events)
	printEvents(events, c.Instance(), nil, cache)
	return nil
}

// eventsAfter returns events that appear after the given event ID (exclusive).
func eventsAfter(events []api.SpaceEvent, afterID string) []api.SpaceEvent {
	for i, ev := range events {
		if ev.ID == afterID {
			return events[i+1:]
		}
	}
	return events // ID not found, return all
}

var sendCmd = &cobra.Command{
	Use:   "send <space> <room> <message...>",
	Short: "Send a message to a room",
	Args:  cobra.MinimumNArgs(3),
	RunE:  runSend,
}

func init() {
	rootCmd.AddCommand(sendCmd)
}

func runSend(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	spaceID, err := resolveSpace(c, args[0])
	if err != nil {
		return err
	}
	roomID, err := resolveRoom(c, spaceID, args[1])
	if err != nil {
		return err
	}

	body := strings.Join(args[2:], " ")

	ev, err := c.PostMessage(spaceID, roomID, body)
	if err != nil {
		return err
	}

	if flagJSON {
		printJSON(ev)
		return nil
	}

	fmt.Printf("Sent (event %s)\n", ev.ID)
	return nil
}

// printEvents renders a list of SpaceEvents to stdout in an IRC-like format.
// instance is prepended to relative attachment URLs.
// roomNames maps room IDs to display names; pass nil to skip room labels.
// cache is used for thread/reply context lookups; pass nil to skip.
func printEvents(events []api.SpaceEvent, instance string, roomNames map[string]string, cache *EventCache) {
	for _, ev := range events {
		tsRaw := "[" + formatTime(ev.CreatedAt) + "]"
		actor := actorName(ev)
		roomName := resolveRoomName(ev.Event.RoomID, roomNames)

		switch ev.Event.TypeName {
		case "MessagePostedEvent":
			body := renderBody(ev.Event, instance)
			nick := "<" + actor + ">"
			thread := ""
			if ev.Event.InThread != "" {
				thread = "thread"
				if orig, ok := cache.Lookup(ev.Event.SpaceID, ev.Event.RoomID, ev.Event.InThread); ok {
					if orig.body != "" {
						thread = "\"" + truncate(strings.ReplaceAll(orig.body, "\n", " "), 40) + "\""
					}
				}
			}
			var ctx []string
			if ev.Event.InReplyTo != "" {
				if orig, ok := cache.Lookup(ev.Event.SpaceID, ev.Event.RoomID, ev.Event.InReplyTo); ok {
					ctx = append(ctx, "↩ "+formatRef(orig))
				}
			}
			var reactions []string
			for _, r := range ev.Event.Reactions {
				reactions = append(reactions, r.Emoji+" "+strconv.Itoa(r.Count))
			}
			cache.store(ev.ID, actor, ev.Event.Body)
			if ev.Event.MessageBodyID != "" {
				cache.storeBodyID(ev.Event.MessageBodyID, ev.ID, ev.Event.RoomID)
			}
			printMsg(tsRaw, roomName, thread, nick, body, ctx, reactions)

		case "MessageUpdatedEvent":
			body := ev.Event.Body
			for _, a := range ev.Event.Attachments {
				body += "\n" + renderAttachment(a, instance)
			}
			printMsg(tsRaw, roomName, "", "<"+actor+">", "[edit] "+body, nil, nil)

		case "MessageDeletedEvent":
			printStatus(tsRaw, roomName, "*** message deleted")

		case "UserJoinedRoomEvent":
			printStatus(tsRaw, roomName, "*** "+actor+" has joined")

		case "UserLeftRoomEvent":
			printStatus(tsRaw, roomName, "*** "+actor+" has left")

		case "ReactionAddedEvent":
			msg := "*** " + actor + " reacted " + ev.Event.Emoji
			if orig, ok := cache.Lookup(ev.Event.SpaceID, ev.Event.RoomID, ev.Event.MessageEventID); ok {
				msg += dim(" → " + formatRef(orig))
			}
			printStatus(tsRaw, roomName, msg)

		case "ReactionRemovedEvent":
			msg := "*** " + actor + " removed reaction " + ev.Event.Emoji
			if orig, ok := cache.Lookup(ev.Event.SpaceID, ev.Event.RoomID, ev.Event.MessageEventID); ok {
				msg += dim(" → " + formatRef(orig))
			}
			printStatus(tsRaw, roomName, msg)

		case "VideoProcessingCompletedEvent":
			msg := "*** video processed"
			if updated, ok := cache.RefetchByBodyID(ev.Event.SpaceID, ev.Event.MessageBodyID); ok {
				parts := []string{}
				for _, a := range updated.Event.Attachments {
					parts = append(parts, renderAttachment(a, instance))
				}
				if len(parts) > 0 {
					msg += ": " + strings.Join(parts, " ")
				}
			}
			printStatus(tsRaw, roomName, msg)
		}

		if flagDebug {
			if len(ev.RawJSON) > 0 {
				fmt.Printf("%s\n", dim(string(ev.RawJSON)))
			} else {
				b, _ := json.Marshal(ev)
				fmt.Printf("%s\n", dim(string(b)))
			}
		}
	}
}

// printMsg prints a message line with proper multi-line continuation indent.
// tsRaw is the timestamp without ANSI, roomName is the plain room name (empty
// to omit), thread is the thread label (empty for non-thread messages),
// nick is the plain nick. body may contain newlines. context lines are printed
// after the body, dimmed. reactions follow.
func printMsg(tsRaw, roomName, thread, nick, body string, context, reactions []string) {
	roomPart := ""
	roomVW := 0
	if roomName != "" {
		roomPart = " " + dim("[#"+roomName+"]")
		roomVW = 2 + len(roomName) + 2 // " [#" + name + "]"
	}
	threadPart := ""
	threadVW := 0
	if thread != "" {
		label := truncate(thread, 40)
		threadPart = " " + dim("[Thread "+label+"]")
		threadVW = 1 + 8 + len(label) + 1 // " [Thread " + label + "]"
	}
	prefixVW := len(tsRaw) + roomVW + threadVW + 1 + len(nick) // ts + room + thread + " " + nick
	indent := strings.Repeat(" ", prefixVW+1)                   // +1 for the space before body

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
		roomPart = " " + dim("[#"+roomName+"]")
	}
	fmt.Printf("%s%s %s\n", dim(tsRaw), roomPart, dim(msg))
}

// resolveRoomName returns the display name for a room ID, or "" if roomNames is nil.
func resolveRoomName(roomID string, roomNames map[string]string) string {
	if roomNames == nil {
		return ""
	}
	if name, ok := roomNames[roomID]; ok {
		return name
	}
	return roomID
}

// renderBody builds the display string for a MessagePostedEvent, appending
// attachment references (Markdown image syntax for images, plain URL otherwise).
func renderBody(ev api.RawEventPayload, instance string) string {
	body := ev.Body
	for _, a := range ev.Attachments {
		if body != "" {
			body += "\n"
		}
		body += renderAttachment(a, instance)
	}
	return body
}

// renderAttachment returns a Markdown image reference for image attachments,
// or a plain URL for other file types. Relative URLs are prefixed with instance.
// For video attachments with completed processing, uses the first variant URL.
func renderAttachment(a api.Attachment, instance string) string {
	resolve := func(u string) string {
		if strings.HasPrefix(u, "/") {
			return strings.TrimRight(instance, "/") + u
		}
		return u
	}

	if a.VideoProcessing != nil && a.VideoProcessing.Status == "COMPLETED" && len(a.VideoProcessing.Variants) > 0 {
		return resolve(a.VideoProcessing.Variants[0].URL)
	}

	url := resolve(a.URL)
	if strings.HasPrefix(a.ContentType, "image/") {
		return "![" + a.Filename + "](" + url + ")"
	}
	return url
}

func formatRef(m cachedMsg) string {
	if m.body == "" {
		return m.actor + ": [attachment]"
	}
	quote := truncate(strings.ReplaceAll(m.body, "\n", " "), 60)
	return m.actor + ": \"" + quote + "\""
}

func actorName(ev api.SpaceEvent) string {
	if ev.Actor != nil {
		if ev.Actor.DisplayName != "" {
			return ev.Actor.DisplayName
		}
		return ev.Actor.Login
	}
	return ev.ActorID
}

func formatTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	now := time.Now()
	if t.Year() == now.Year() && t.Month() == now.Month() && t.Day() == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}
