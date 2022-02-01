package shared

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/matrix-org/dendrite/roomserver/types"
	"github.com/matrix-org/gomatrixserverlib"
)

type RoomUpdater struct {
	transaction
	d                       *Database
	roomInfo                types.RoomInfo
	latestEvents            []types.StateAtEventAndReference
	lastEventIDSent         string
	currentStateSnapshotNID types.StateSnapshotNID
}

func rollback(txn *sql.Tx) {
	if txn == nil {
		return
	}
	txn.Rollback() // nolint: errcheck
}

func NewRoomUpdater(ctx context.Context, d *Database, txn *sql.Tx, roomInfo types.RoomInfo) (*RoomUpdater, error) {
	eventNIDs, lastEventNIDSent, currentStateSnapshotNID, err :=
		d.RoomsTable.SelectLatestEventsNIDsForUpdate(ctx, txn, roomInfo.RoomNID)
	if err != nil {
		rollback(txn)
		return nil, err
	}
	stateAndRefs, err := d.EventsTable.BulkSelectStateAtEventAndReference(ctx, txn, eventNIDs)
	if err != nil {
		rollback(txn)
		return nil, err
	}
	var lastEventIDSent string
	if lastEventNIDSent != 0 {
		lastEventIDSent, err = d.EventsTable.SelectEventID(ctx, txn, lastEventNIDSent)
		if err != nil {
			rollback(txn)
			return nil, err
		}
	}
	return &RoomUpdater{
		transaction{ctx, txn}, d, roomInfo, stateAndRefs, lastEventIDSent, currentStateSnapshotNID,
	}, nil
}

// RoomVersion implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) RoomVersion() (version gomatrixserverlib.RoomVersion) {
	return u.roomInfo.RoomVersion
}

// LatestEvents implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) LatestEvents() []types.StateAtEventAndReference {
	return u.latestEvents
}

// LastEventIDSent implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) LastEventIDSent() string {
	return u.lastEventIDSent
}

// CurrentStateSnapshotNID implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) CurrentStateSnapshotNID() types.StateSnapshotNID {
	return u.currentStateSnapshotNID
}

// StorePreviousEvents implements types.RoomRecentEventsUpdater - This must be called from a Writer
func (u *RoomUpdater) StorePreviousEvents(eventNID types.EventNID, previousEventReferences []gomatrixserverlib.EventReference) error {
	for _, ref := range previousEventReferences {
		if err := u.d.PrevEventsTable.InsertPreviousEvent(u.ctx, u.txn, ref.EventID, ref.EventSHA256, eventNID); err != nil {
			return fmt.Errorf("u.d.PrevEventsTable.InsertPreviousEvent: %w", err)
		}
	}
	return nil
}

func (u *RoomUpdater) StoreEvent(
	ctx context.Context, event *gomatrixserverlib.Event,
	authEventNIDs []types.EventNID, isRejected bool,
) (types.EventNID, types.RoomNID, types.StateAtEvent, *gomatrixserverlib.Event, string, error) {
	return u.d.storeEvent(ctx, u.txn, event, authEventNIDs, isRejected)
}

func (u *RoomUpdater) AddState(
	ctx context.Context,
	roomNID types.RoomNID,
	stateBlockNIDs []types.StateBlockNID,
	state []types.StateEntry,
) (stateNID types.StateSnapshotNID, err error) {
	return u.d.addState(ctx, u.txn, roomNID, stateBlockNIDs, state)
}

func (u *RoomUpdater) SetState(
	ctx context.Context, eventNID types.EventNID, stateNID types.StateSnapshotNID,
) error {
	return u.d.Writer.Do(u.d.DB, u.txn, func(txn *sql.Tx) error {
		return u.d.EventsTable.UpdateEventState(ctx, txn, eventNID, stateNID)
	})
}

func (u *RoomUpdater) RoomInfo(ctx context.Context, roomID string) (*types.RoomInfo, error) {
	return u.d.roomInfo(ctx, u.txn, roomID)
}

func (u *RoomUpdater) StateAtEventIDs(
	ctx context.Context, eventIDs []string,
) ([]types.StateAtEvent, error) {
	return u.d.EventsTable.BulkSelectStateAtEventByID(ctx, u.txn, eventIDs)
}

func (u *RoomUpdater) StateEntriesForEventIDs(
	ctx context.Context, eventIDs []string,
) ([]types.StateEntry, error) {
	return u.d.EventsTable.BulkSelectStateEventByID(ctx, u.txn, eventIDs)
}

func (u *RoomUpdater) EventsFromIDs(ctx context.Context, eventIDs []string) ([]types.Event, error) {
	return u.d.eventsFromIDs(ctx, u.txn, eventIDs)
}

func (u *RoomUpdater) GetMembershipEventNIDsForRoom(
	ctx context.Context, roomNID types.RoomNID, joinOnly bool, localOnly bool,
) ([]types.EventNID, error) {
	return u.d.getMembershipEventNIDsForRoom(ctx, u.txn, roomNID, joinOnly, localOnly)
}

// IsReferenced implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) IsReferenced(eventReference gomatrixserverlib.EventReference) (bool, error) {
	err := u.d.PrevEventsTable.SelectPreviousEventExists(u.ctx, u.txn, eventReference.EventID, eventReference.EventSHA256)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("u.d.PrevEventsTable.SelectPreviousEventExists: %w", err)
}

// SetLatestEvents implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) SetLatestEvents(
	roomNID types.RoomNID, latest []types.StateAtEventAndReference, lastEventNIDSent types.EventNID,
	currentStateSnapshotNID types.StateSnapshotNID,
) error {
	eventNIDs := make([]types.EventNID, len(latest))
	for i := range latest {
		eventNIDs[i] = latest[i].EventNID
	}
	return u.d.Writer.Do(u.d.DB, u.txn, func(txn *sql.Tx) error {
		if err := u.d.RoomsTable.UpdateLatestEventNIDs(u.ctx, txn, roomNID, eventNIDs, lastEventNIDSent, currentStateSnapshotNID); err != nil {
			return fmt.Errorf("u.d.RoomsTable.updateLatestEventNIDs: %w", err)
		}
		if roomID, ok := u.d.Cache.GetRoomServerRoomID(roomNID); ok {
			if roomInfo, ok := u.d.Cache.GetRoomInfo(roomID); ok {
				roomInfo.StateSnapshotNID = currentStateSnapshotNID
				roomInfo.IsStub = false
				u.d.Cache.StoreRoomInfo(roomID, roomInfo)
			}
		}
		return nil
	})
}

// HasEventBeenSent implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) HasEventBeenSent(eventNID types.EventNID) (bool, error) {
	return u.d.EventsTable.SelectEventSentToOutput(u.ctx, u.txn, eventNID)
}

// MarkEventAsSent implements types.RoomRecentEventsUpdater
func (u *RoomUpdater) MarkEventAsSent(eventNID types.EventNID) error {
	return u.d.Writer.Do(u.d.DB, u.txn, func(txn *sql.Tx) error {
		return u.d.EventsTable.UpdateEventSentToOutput(u.ctx, txn, eventNID)
	})
}

func (u *RoomUpdater) MembershipUpdater(targetUserNID types.EventStateKeyNID, targetLocal bool) (*MembershipUpdater, error) {
	return u.d.membershipUpdaterTxn(u.ctx, u.txn, u.roomInfo.RoomNID, targetUserNID, targetLocal)
}