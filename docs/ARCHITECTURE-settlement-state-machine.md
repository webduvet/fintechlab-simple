# Architecture: Settlement State Machine

## Goal

Define and implement a state machine for settlement records that governs the lifecycle of a daily settlement from generation through to final posting. This mirrors the settlement lifecycle observed in Worldline's processing model where settlement states progress through scheduling, processing, and posting.

## Worldline Reference Model

Worldline's North American settlement API returns `settlement_state` values including `"Scheduled"`, `"On Hold"`, `"Superseded"`, `"In Process"`, `"Approved"`, `"Declined"`, `"Pending Payment"`, `"Full Payment"`. Settlement dates have a midnight cut-off (Pacific Time). Reports are generated for up to a 3-month range.

In the Worldline GlobalCollect model, capture involves gathering electronic payment reports from banks daily. Transactions received by a certain cut-off time are processed same-day. Payments are reported as paid only after funds have cleared. Clearing time varies from 2-7 days depending on bank, card type, and currency.

This architecture models the core lifecycle: Scheduled → Processing → Settled (or Failed).

## Scope

- New `internal/settlement/state.go` — state machine definition and transition logic
- Integration with `internal/settlement/store.go` — state persisted alongside settlement records
- Transition events triggered by the batch generation job and reconciliation API
- State validation — invalid transitions return an error

## What This Is Not

- Not a replica of Worldline's full state model (too many states for the lab)
- Not a payment-level state machine (that's the payment API's job)
- No external event sourcing or CQRS

## State Definitions

```go
type SettlementState string

const (
    StateScheduled   SettlementState = "Scheduled"   // Report generated, awaiting processing
    StateProcessing  SettlementState = "Processing"  // Processing in progress (e.g., fee calculation, reserve hold)
    StateSettled     SettlementState = "Settled"     // Settlement complete, funds credited
    StateFailed      SettlementState = "Failed"      // Settlement failed, needs manual review
    StateSuperseded  SettlementState = "Superseded"  // Replaced by a later settlement (e.g., correction)
)
```

## State Transition Diagram

```
                    ┌─────────────┐
                    │  Scheduled  │
                    └──────┬──────┘
                           │ trigger_process
                           ▼
                    ┌─────────────┐
                    │ Processing  │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │ success    │    failure  │
              ▼            │             ▼
       ┌──────────┐       │      ┌──────────┐
       │  Settled  │       │      │  Failed  │
       └──────────┘       │      └──────────┘
              │            │
              │ superseded │
              ▼            │
       ┌──────────┐       │
       │Superseded│◄──────┘
       └──────────┘
```

## Valid Transitions

| From | To | Trigger | Conditions |
| --- | --- | --- | --- |
| `Scheduled` | `Processing` | `TriggerProcessing` | Always valid |
| `Processing` | `Settled` | `Settle` | Report data must be present |
| `Processing` | `Failed` | `Fail` | Reason string required |
| `Failed` | `Scheduled` | `Retry` | Manual re-schedule |
| `Settled` | `Superseded` | `Supersede` | Replacement record ID required |
| `Superseded` | `Scheduled` | `Reactivate` | Manual re-activation |

Any transition not listed above is invalid and returns an error.

## Components

### 1. `internal/settlement/state.go`

```go
// TransitionResult describes the outcome of a state transition.
type TransitionResult struct {
    RecordID    string           `json:"record_id"`
    FromState   SettlementState  `json:"from_state"`
    ToState     SettlementState  `json:"to_state"`
    Success     bool             `json:"success"`
    Error       string           `json:"error,omitempty"`
}

// Transition attempts to move a settlement record from its current state to a new state.
// Returns TransitionResult indicating success or failure.
func (s *Store) Transition(id string, event TransitionEvent) (TransitionResult, error)

// ValidTransitions returns the set of allowed next states for a given current state.
func ValidTransitions(current SettlementState) []SettlementState

// CanTransition returns true if the transition from -> to is valid.
func CanTransition(from, to SettlementState) bool

// CanTransition returns true if the transition from -> to is valid.
func CanTransition(from, to SettlementState) bool
```

**TransitionEvent types:**

```go
type TransitionEvent string

const (
    EventProcess    TransitionEvent = "process"     // Scheduled → Processing
    EventSettle     TransitionEvent = "settle"      // Processing → Settled
    EventFail       TransitionEvent = "fail"        // Processing → Failed
    EventRetry      TransitionEvent = "retry"       // Failed → Scheduled
    EventSupersede  TransitionEvent = "supersede"   // Settled → Superseded
    EventReactivate TransitionEvent = "reactivate"  // Superseded → Scheduled
)
```

### 2. State transition validation logic

```go
var transitions = map[SettlementState]map[TransitionEvent]SettlementState{
    StateScheduled: {
        EventProcess: StateProcessing,
    },
    StateProcessing: {
        EventSettle:  StateSettled,
        EventFail:    StateFailed,
    },
    StateFailed: {
        EventRetry:   StateScheduled,
    },
    StateSettled: {
        EventSupersede: StateSuperseded,
    },
    StateSuperseded: {
        EventReactivate: StateScheduled,
    },
}
```

### 3. Batch job integration

The settlement batch generation job (`cmd/settlement/main.go`) drives the state machine:

```go
// Step 1: Generate creates a SettlementRecord in StateScheduled
record := &SettlementRecord{
    State: StateScheduled,
    ...
}
store.Create(record)

// Step 2: Batch processing transitions to Processing
store.Transition(record.ID, EventProcess)

// Step 3: After all processing (fees, reserves), transition to Settled
store.Transition(record.ID, EventSettle)
```

### 4. Reconciliation API integration

The `POST /reports/settlement/generate` endpoint triggers the full lifecycle:

```
POST /reports/settlement/generate
  → Create record (Scheduled)
  → Transition to Processing
  → Run Generate() on ledger data
  → Transition to Settled (or Failed on error)
  → Stage files to SFTP
  → Persist store
```

### 5. Additional endpoint for manual state control

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/reports/settlement/{id}/transition` | POST | Manually trigger a state transition |

Request body:

```json
{
  "event": "retry",
  "reason": "manual re-schedule after review"
}
```

Response:

```json
{
  "record_id": "set_abc123",
  "from_state": "Failed",
  "to_state": "Scheduled",
  "success": true
}
```

## Error Conditions

| Condition | Error |
| --- | --- |
| Invalid transition | `settlement: invalid transition from {current} to {requested}` |
| Record not found | `settlement: record {id} not found` |
| Settle without report data | `settlement: cannot settle without report data` |
| Supersede without replacement | `settlement: supersede requires replacement record ID` |

## Testing

- Unit test all valid transitions
- Unit test all invalid transitions return errors
- Unit test `ValidTransitions()` returns correct set for each state
- Integration test: generate → process → settle → verify state
- Integration test: fail → retry → verify state
- Integration test: settle → supersede → verify state
- Test that store persistence preserves state across reloads

## Existing Patterns Followed

- `sync.Mutex` for concurrent access
- `fmt.Errorf` for error messages
- Same `env()` pattern
- Standard library only
- Same package structure (`internal/settlement/`)

## See Also

- `ARCHITECTURE-settlement-report.md` — report generation
- `ARCHITECTURE-sftp-staging.md` — file staging
- `ARCHITECTURE-reconciliation-api.md` — HTTP query endpoint
