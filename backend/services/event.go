package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"middle-monitor/backend/models"
)

// EventService handles event operations
type EventService struct {
	db *sql.DB
}

func NewEventService(db *sql.DB) *EventService {
	return &EventService{db: db}
}

func (s *EventService) CreateEvent(event models.Event) (*models.Event, error) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Default to organization 1 if not specified (for backward compatibility)
	if event.OrganizationID == 0 {
		event.OrganizationID = 1
	}

	query := `INSERT INTO events (organization_id, type, service, message, metadata, timestamp)
			  VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`

	var id int64
	err := s.db.QueryRow(query, event.OrganizationID, event.Type, event.Service, event.Message, event.Metadata, event.Timestamp).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEventCreate, err)
	}

	event.ID = id
	return &event, nil
}

func (s *EventService) GetEvents(service string, limit int) ([]models.Event, error) {
	return s.GetEventsForOrg(0, service, limit)
}

func (s *EventService) GetEventsForOrg(orgID int64, service string, limit int) ([]models.Event, error) {
	return s.GetEventsForOrgFiltered(orgID, service, limit, time.Time{}, time.Time{})
}

func (s *EventService) GetEventsForOrgFiltered(orgID int64, service string, limit int, from, to time.Time) ([]models.Event, error) {
	query := `SELECT id, organization_id, type, service, message, metadata, timestamp AT TIME ZONE 'UTC' as timestamp
			  FROM events WHERE 1=1`
	args := []interface{}{}
	argPos := 1

	if orgID > 0 {
		query += " AND organization_id = $" + strconv.Itoa(argPos)
		args = append(args, orgID)
		argPos++
	}
	if service != "" {
		query += " AND service = $" + strconv.Itoa(argPos)
		args = append(args, service)
		argPos++
	}
	if !from.IsZero() {
		query += " AND timestamp >= $" + strconv.Itoa(argPos)
		args = append(args, from.UTC())
		argPos++
	}
	if !to.IsZero() {
		query += " AND timestamp <= $" + strconv.Itoa(argPos)
		args = append(args, to.UTC())
		argPos++
	}

	query += " ORDER BY timestamp DESC LIMIT $" + strconv.Itoa(argPos)
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEventsFetch, err)
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var e models.Event
		var metadata sql.NullString
		var orgIDNull sql.NullInt64
		if err := rows.Scan(&e.ID, &orgIDNull, &e.Type, &e.Service, &e.Message, &metadata, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrEventScan, err)
		}
		e.Timestamp = e.Timestamp.UTC()
		if orgIDNull.Valid {
			e.OrganizationID = orgIDNull.Int64
		}
		if metadata.Valid {
			e.Metadata = &metadata.String
		}
		events = append(events, e)
	}

	return events, nil
}
