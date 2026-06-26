package main

import (
	"autotask-easymode/internal/atapi"
	"context"
)

func (a *App) ensureJournalTicket(ctx context.Context, client autotaskClient, journal *writeJournal, rec *writeJournalRecord, existingID int64, createTicket map[string]any, validateContact bool) (int64, error) {
	if rec.TicketID != 0 {
		return rec.TicketID, nil
	}
	ticketID := existingID
	if createTicket != nil {
		if validateContact {
			if err := validateCreateTicketContact(ctx, client, createTicket); err != nil {
				return 0, err
			}
		}
		id, err := client.Create(ctx, atapi.EntityTickets, createTicket)
		if err != nil {
			return 0, err
		}
		ticketID = id
	}
	rec.TicketID = ticketID
	return ticketID, journal.touch(rec, a.now())
}

func (a *App) ensureJournalTimeEntry(ctx context.Context, client autotaskClient, journal *writeJournal, rec *writeJournalRecord, ticketID int64, index int, entry map[string]any) (int64, error) {
	if rec.EntryIDs[index] != 0 {
		return rec.EntryIDs[index], nil
	}
	entry["ticketID"] = ticketID
	id, err := client.Create(ctx, atapi.EntityTimeEntries, entry)
	if err != nil {
		return 0, err
	}
	rec.EntryIDs[index] = id
	return id, journal.touch(rec, a.now())
}

func (a *App) ensureJournalClosed(ctx context.Context, client autotaskClient, journal *writeJournal, rec *writeJournalRecord, ticketID int64, closeTicket bool) (bool, error) {
	if !closeTicket {
		return false, nil
	}
	if rec.Closed {
		return true, nil
	}
	status, err := a.completeTicketStatus()
	if err != nil {
		return false, err
	}
	if _, err := client.Update(ctx, atapi.EntityTickets, map[string]any{"id": ticketID, "status": status}); err != nil {
		return false, err
	}
	rec.Closed = true
	if err := journal.touch(rec, a.now()); err != nil {
		return false, err
	}
	return true, nil
}
