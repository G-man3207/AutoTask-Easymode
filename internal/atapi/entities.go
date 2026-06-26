package atapi

import (
	"context"
	"net/http"
)

// Entity name constants used across the app.
const (
	EntityCompanies    = "Companies"
	EntityContacts     = "Contacts"
	EntityTickets      = "Tickets"
	EntityTimeEntries  = "TimeEntries"
	EntityResources    = "Resources"
	EntityBillingCodes = "BillingCodes"
)

// AllCompanies is the SearchTickets companyID sentinel meaning "do not scope to
// a company". It is negative because 0 is a valid company id (the owner org).
const AllCompanies = -1

// Field describes one entity field from the entityInformation/fields endpoint.
type Field struct {
	Name           string          `json:"name"`
	IsPickList     bool            `json:"isPickList"`
	PicklistValues []PicklistValue `json:"picklistValues"`
}

// PicklistValue is one allowed value of a picklist field (e.g. a ticket status).
type PicklistValue struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	ParentValue string `json:"parentValue"`
	IsActive    bool   `json:"isActive"`
	IsDefault   bool   `json:"isDefaultValue"`
}

// EntityFields returns the field metadata for an entity, including the allowed
// values of its picklist fields (status, priority, queue, …).
func (c *Client) EntityFields(ctx context.Context, entity string) ([]Field, error) {
	var r struct {
		Fields []Field `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, c.entityURL(entity, "entityInformation", "fields"), nil, &r); err != nil {
		return nil, err
	}
	return r.Fields, nil
}

// BillingCodes returns active billing codes (the work types used on time entries).
func (c *Client) BillingCodes(ctx context.Context, limit int) ([]map[string]any, error) {
	return c.Query(ctx, EntityBillingCodes, []Filter{
		{Op: "eq", Field: "isActive", Value: true},
	}, limit)
}

// SearchCompanies finds active companies whose name contains q (case-insensitive).
func (c *Client) SearchCompanies(ctx context.Context, q string, limit int) ([]map[string]any, error) {
	return c.Query(ctx, EntityCompanies, []Filter{
		{Op: "contains", Field: "companyName", Value: q},
	}, limit)
}

// SearchContacts finds active contacts for one company by name or email.
func (c *Client) SearchContacts(ctx context.Context, q string, companyID, limit int) ([]map[string]any, error) {
	return c.Query(ctx, EntityContacts, []Filter{
		{Op: "eq", Field: "companyID", Value: companyID},
		{Op: "eq", Field: "isActive", Value: 1},
		{Op: "or", Items: []Filter{
			{Op: "contains", Field: "firstName", Value: q},
			{Op: "contains", Field: "lastName", Value: q},
			{Op: "contains", Field: "emailAddress", Value: q},
		}},
	}, limit)
}

// SearchResources finds resources matching q against first name, last name, or
// email. Useful for discovering your own resourceID.
func (c *Client) SearchResources(ctx context.Context, q string, limit int) ([]map[string]any, error) {
	return c.Query(ctx, EntityResources, []Filter{
		{Op: "or", Items: []Filter{
			{Op: "contains", Field: "firstName", Value: q},
			{Op: "contains", Field: "lastName", Value: q},
			{Op: "contains", Field: "email", Value: q},
		}},
	}, limit)
}

// TicketsForCompany returns tickets belonging to a company.
func (c *Client) TicketsForCompany(ctx context.Context, companyID, limit int) ([]map[string]any, error) {
	return c.Query(ctx, EntityTickets, []Filter{
		{Op: "eq", Field: "companyID", Value: companyID},
	}, limit)
}

// SearchTickets finds tickets whose title contains q, optionally scoped to a
// single company. Pass AllCompanies to search across every company; any
// non-negative companyID (including 0, the owner org) scopes to that company.
func (c *Client) SearchTickets(ctx context.Context, q string, companyID, limit int) ([]map[string]any, error) {
	filters := []Filter{{Op: "contains", Field: "title", Value: q}}
	if companyID >= 0 {
		filters = append(filters, Filter{Op: "eq", Field: "companyID", Value: companyID})
	}
	return c.Query(ctx, EntityTickets, filters, limit)
}

// TimeEntriesForTickets returns time entries logged against any of ticketIDs,
// optionally bounded by a [from,to] dateWorked window (RFC3339 strings; empty to skip).
func (c *Client) TimeEntriesForTickets(ctx context.Context, ticketIDs []int64, from, to string, limit int) ([]map[string]any, error) {
	items := []Filter{{Op: "in", Field: "ticketID", Value: ticketIDs}}
	if from != "" {
		items = append(items, Filter{Op: "gte", Field: "dateWorked", Value: from})
	}
	if to != "" {
		items = append(items, Filter{Op: "lte", Field: "dateWorked", Value: to})
	}
	return c.Query(ctx, EntityTimeEntries, items, limit)
}
