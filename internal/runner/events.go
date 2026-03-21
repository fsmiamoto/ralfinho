// Package runner implements the ralfinho agent iteration loop.
//
// Event types are defined in internal/events and re-exported here as type
// aliases for backward compatibility.
package runner

import "github.com/fsmiamoto/ralfinho/internal/events"

// Type aliases — these preserve backward compatibility so that existing code
// using runner.Event, runner.EventSession, etc. continues to compile.

type EventType = events.EventType

const (
	EventSession             = events.EventSession
	EventAgentStart          = events.EventAgentStart
	EventTurnStart           = events.EventTurnStart
	EventMessageStart        = events.EventMessageStart
	EventMessageUpdate       = events.EventMessageUpdate
	EventMessageEnd          = events.EventMessageEnd
	EventToolExecutionStart  = events.EventToolExecutionStart
	EventToolExecutionUpdate = events.EventToolExecutionUpdate
	EventToolExecutionEnd    = events.EventToolExecutionEnd
	EventTurnEnd             = events.EventTurnEnd
	EventAgentEnd            = events.EventAgentEnd
	EventIteration           = events.EventIteration
	EventInactivityTimeout   = events.EventInactivityTimeout
	EventRateLimit           = events.EventRateLimit
)

type Event = events.Event
type MessageEnvelope = events.MessageEnvelope
type AssistantEvent = events.AssistantEvent
type ContentBlock = events.ContentBlock
type ToolArgs = events.ToolArgs
type RateLimitInfo = events.RateLimitInfo
