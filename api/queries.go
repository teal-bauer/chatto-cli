package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// User represents a chatto user.
type User struct {
	ID             string `json:"id"`
	Login          string `json:"login"`
	DisplayName    string `json:"displayName"`
	AvatarURL      string `json:"avatarUrl"`
	PresenceStatus string `json:"presenceStatus"`
}

// Space represents a chatto space.
type Space struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ViewerIsMember bool   `json:"viewerIsMember"`
}

// Room represents a room within a space.
type Room struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
	Joined   bool   `json:"joined"` // resolved client-side
}

// Reaction on a message.
type Reaction struct {
	Emoji      string `json:"emoji"`
	Count      int    `json:"count"`
	HasReacted bool   `json:"hasReacted"`
}

// VideoVariant is a processed video quality variant.
type VideoVariant struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

// VideoProcessing holds the processing state of a video attachment.
type VideoProcessing struct {
	Status       string         `json:"status"` // PENDING, PROCESSING, COMPLETED, FAILED
	Variants     []VideoVariant `json:"variants"`
	ThumbnailURL string         `json:"thumbnailUrl"`
}

// Attachment on a message.
type Attachment struct {
	ID              string           `json:"id"`
	Filename        string           `json:"filename"`
	ContentType     string           `json:"contentType"`
	Size            int              `json:"size"`
	URL             string           `json:"url"`
	VideoProcessing *VideoProcessing `json:"videoProcessing"`
}

// MessagePostedEvent is the event payload for a new message.
type MessagePostedEvent struct {
	SpaceID       string       `json:"spaceId"`
	RoomID        string       `json:"roomId"`
	Body          string       `json:"body"`
	MessageBodyID string       `json:"messageBodyId"`
	InReplyTo     string       `json:"inReplyTo"`
	InThread      string       `json:"inThread"`
	Reactions     []Reaction   `json:"reactions"`
	Attachments   []Attachment `json:"attachments"`
	ReplyCount    int          `json:"replyCount"`
	UpdatedAt     string       `json:"updatedAt"`
}

// SpaceEvent is an event in the event stream.
type SpaceEvent struct {
	ID         string          `json:"id"`
	CreatedAt  string          `json:"createdAt"`
	ActorID    string          `json:"actorId"`
	SequenceID string          `json:"sequenceId"`
	Actor      *User           `json:"actor"`
	Event      RawEventPayload `json:"event"`
	RawJSON    json.RawMessage `json:"-"` // raw server bytes, populated where available
}

// RawEventPayload holds the typed event data. We keep it as raw JSON for flexible parsing.
type RawEventPayload struct {
	TypeName string `json:"__typename"`
	// Message events
	SpaceID       string       `json:"spaceId"`
	RoomID        string       `json:"roomId"`
	Body          string       `json:"body"`
	MessageBodyID string       `json:"messageBodyId"`
	MessageEventID string      `json:"messageEventId"`
	InReplyTo     string       `json:"inReplyTo"`
	InThread      string       `json:"inThread"`
	Reactions     []Reaction   `json:"reactions"`
	Attachments   []Attachment `json:"attachments"`
	ReplyCount    int          `json:"replyCount"`
	// Reaction events
	Emoji string `json:"emoji"`
	// VideoProcessingCompletedEvent
	AttachmentID string `json:"attachmentId"`
	// UserTypingEvent
	ThreadRootEventID string `json:"threadRootEventId"`
}

// Me returns the authenticated user.
func (c *Client) Me() (*User, error) {
	var data struct {
		Me *User `json:"me"`
	}
	err := c.Execute(`{ me { id login displayName avatarUrl presenceStatus } }`, nil, &data)
	return data.Me, err
}

// GetSpaces returns all spaces visible to the user.
func (c *Client) GetSpaces() ([]Space, error) {
	var data struct {
		Spaces []Space `json:"spaces"`
	}
	err := c.Execute(`{ spaces { id name viewerIsMember } }`, nil, &data)
	return data.Spaces, err
}

// GetRooms returns rooms in a space with membership status.
func (c *Client) GetRooms(spaceID string) ([]Room, error) {
	var data struct {
		Space *struct {
			Rooms []Room `json:"rooms"`
		} `json:"space"`
		Me *struct {
			RoomMemberships []struct {
				Room struct {
					ID string `json:"id"`
				} `json:"room"`
			} `json:"roomMemberships"`
		} `json:"me"`
	}
	err := c.Execute(`
		query GetRooms($spaceId: ID!) {
			space(id: $spaceId) {
				rooms { id name archived }
			}
			me {
				roomMemberships(spaceId: $spaceId) { room { id } }
			}
		}`, map[string]any{"spaceId": spaceID}, &data)
	if err != nil {
		return nil, err
	}
	if data.Space == nil {
		return nil, nil
	}
	joinedIDs := map[string]bool{}
	if data.Me != nil {
		for _, m := range data.Me.RoomMemberships {
			joinedIDs[m.Room.ID] = true
		}
	}
	var rooms []Room
	for _, r := range data.Space.Rooms {
		if r.Archived {
			continue
		}
		r.Joined = joinedIDs[r.ID]
		rooms = append(rooms, r)
	}
	return rooms, nil
}

const attachmentFields = `id filename contentType size url videoProcessing { status variants { url quality } }`

const spaceEventFields = `
	id createdAt actorId sequenceId
	actor { id login displayName }
	event {
		__typename
		... on MessagePostedEvent {
			spaceId roomId body messageBodyId
			inReplyTo inThread replyCount
			reactions { emoji count hasReacted }
			attachments { ` + attachmentFields + ` }
		}
		... on MessageUpdatedEvent { spaceId roomId body messageBodyId attachments { ` + attachmentFields + ` } }
		... on MessageDeletedEvent { spaceId roomId messageBodyId }
		... on ReactionAddedEvent { spaceId roomId messageEventId emoji }
		... on ReactionRemovedEvent { spaceId roomId messageEventId emoji }
		... on UserJoinedRoomEvent { spaceId roomId }
		... on UserLeftRoomEvent { spaceId roomId }
		... on UserTypingEvent { spaceId roomId threadRootEventId }
		... on VideoProcessingCompletedEvent { spaceId roomId attachmentId messageBodyId }
	}`

// GetRoomEvents returns recent events from a room.
func (c *Client) GetRoomEvents(spaceID, roomID string, limit int) ([]SpaceEvent, error) {
	const q = `
		query RoomEvents($spaceId: ID!, $roomId: ID!, $limit: Int) {
			roomEvents(spaceId: $spaceId, roomId: $roomId, limit: $limit) {` + spaceEventFields + `
			}
		}`
	vars := map[string]any{"spaceId": spaceID, "roomId": roomID, "limit": limit}
	raw, err := c.ExecuteRaw(q, vars)
	if err != nil {
		return nil, err
	}
	var typed struct {
		RoomEvents []SpaceEvent `json:"roomEvents"`
	}
	var rawEvents struct {
		RoomEvents []json.RawMessage `json:"roomEvents"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &rawEvents)
	for i := range typed.RoomEvents {
		if i < len(rawEvents.RoomEvents) {
			typed.RoomEvents[i].RawJSON = rawEvents.RoomEvents[i]
		}
	}
	return typed.RoomEvents, nil
}

// PostMessage sends a message and returns the resulting SpaceEvent.
func (c *Client) PostMessage(spaceID, roomID, body string) (*SpaceEvent, error) {
	var data struct {
		PostMessage *SpaceEvent `json:"postMessage"`
	}
	err := c.Execute(`
		mutation PostMessage($input: PostMessageInput!) {
			postMessage(input: $input) {
				id sequenceId createdAt actorId
				actor { id login displayName }
				event {
					... on MessagePostedEvent {
						spaceId roomId body messageBodyId
					}
				}
			}
		}`, map[string]any{
		"input": map[string]any{
			"spaceId": spaceID,
			"roomId":  roomID,
			"body":    body,
		},
	}, &data)
	return data.PostMessage, err
}

// JoinRoom joins a room.
func (c *Client) JoinRoom(spaceID, roomID string) error {
	var data struct {
		JoinRoom bool `json:"joinRoom"`
	}
	return c.Execute(`
		mutation JoinRoom($spaceId: ID!, $roomId: ID!) {
			joinRoom(spaceId: $spaceId, roomId: $roomId)
		}`, map[string]any{"spaceId": spaceID, "roomId": roomID}, &data)
}

// LeaveRoom leaves a room.
func (c *Client) LeaveRoom(spaceID, roomID string) error {
	var data struct {
		LeaveRoom bool `json:"leaveRoom"`
	}
	return c.Execute(`
		mutation LeaveRoom($spaceId: ID!, $roomId: ID!) {
			leaveRoom(spaceId: $spaceId, roomId: $roomId)
		}`, map[string]any{"spaceId": spaceID, "roomId": roomID}, &data)
}

// JoinSpace joins a space.
func (c *Client) JoinSpace(spaceID string) error {
	var data struct {
		JoinSpace bool `json:"joinSpace"`
	}
	return c.Execute(`
		mutation JoinSpace($spaceId: ID!) {
			joinSpace(spaceId: $spaceId)
		}`, map[string]any{"spaceId": spaceID}, &data)
}

// LeaveSpace leaves a space.
func (c *Client) LeaveSpace(spaceID string) error {
	var data struct {
		LeaveSpace bool `json:"leaveSpace"`
	}
	return c.Execute(`
		mutation LeaveSpace($spaceId: ID!) {
			leaveSpace(spaceId: $spaceId)
		}`, map[string]any{"spaceId": spaceID}, &data)
}

// SearchMembers searches for members in a space.
func (c *Client) SearchMembers(spaceID, search string, limit int) ([]User, error) {
	var data struct {
		Space *struct {
			Members struct {
				Users []User `json:"users"`
			} `json:"members"`
		} `json:"space"`
	}
	err := c.Execute(`
		query SearchMembers($spaceId: ID!, $search: String!, $limit: Int) {
			space(id: $spaceId) {
				members(search: $search, limit: $limit) {
					users { id login displayName }
				}
			}
		}`, map[string]any{"spaceId": spaceID, "search": search, "limit": limit}, &data)
	if err != nil || data.Space == nil {
		return nil, err
	}
	return data.Space.Members.Users, nil
}

// GetEventByID fetches a single room event by event ID.
func (c *Client) GetEventByID(spaceID, roomID, eventID string) (*SpaceEvent, error) {
	const q = `
		query GetEventByID($spaceId: ID!, $roomId: ID!, $eventId: ID!) {
			roomEventByEventId(spaceId: $spaceId, roomId: $roomId, eventId: $eventId) {` + spaceEventFields + `
			}
		}`
	vars := map[string]any{"spaceId": spaceID, "roomId": roomID, "eventId": eventID}
	raw, err := c.ExecuteRaw(q, vars)
	if err != nil {
		return nil, err
	}
	var typed struct {
		RoomEventByEventId *SpaceEvent `json:"roomEventByEventId"`
	}
	var rawEvent struct {
		RoomEventByEventId json.RawMessage `json:"roomEventByEventId"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return nil, err
	}
	if typed.RoomEventByEventId != nil {
		_ = json.Unmarshal(raw, &rawEvent)
		typed.RoomEventByEventId.RawJSON = rawEvent.RoomEventByEventId
	}
	return typed.RoomEventByEventId, nil
}

// ResolveSpaceID looks up a space by ID or name. Returns ID on success.
func (c *Client) ResolveSpaceID(idOrName string) (string, error) {
	spaces, err := c.GetSpaces()
	if err != nil {
		return "", err
	}
	for _, s := range spaces {
		if s.ID == idOrName || strings.EqualFold(s.Name, idOrName) {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("space %q not found", idOrName)
}

// ResolveRoomID looks up a room by ID or name within a space. Returns ID on success.
func (c *Client) ResolveRoomID(spaceID, idOrName string) (string, error) {
	rooms, err := c.GetRooms(spaceID)
	if err != nil {
		return "", err
	}
	for _, r := range rooms {
		if r.ID == idOrName || strings.EqualFold(r.Name, idOrName) || strings.EqualFold("#"+r.Name, idOrName) {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("room %q not found in space %s", idOrName, spaceID)
}
