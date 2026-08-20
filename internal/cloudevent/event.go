package cloudevent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const DataContentTypeJSON = "application/json"

type Event struct {
	Type            string         `json:"type"`
	Source          string         `json:"source"`
	Subject         *string        `json:"subject"`
	ID              string         `json:"id"`
	OrganisationID  *string        `json:"organisation_id"`
	Time            time.Time      `json:"time"`
	DataContentType string         `json:"datacontenttype"`
	Data            map[string]any `json:"data"`
}

func FromJSON(raw string) (*Event, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("invalid CloudEvent JSON: %w", err)
	}
	return FromMap(payload)
}

func FromMap(payload map[string]any) (*Event, error) {
	eventType, ok := payload["type"].(string)
	if !ok || strings.TrimSpace(eventType) == "" {
		return nil, fmt.Errorf("CloudEvent type must not be blank")
	}

	source, ok := payload["source"].(string)
	if !ok || strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("CloudEvent source must not be blank")
	}

	id, _ := payload["id"].(string)
	if strings.TrimSpace(id) == "" {
		id = generateID()
	}

	var subject *string
	if value, ok := payload["subject"].(string); ok && strings.TrimSpace(value) != "" {
		trimmed := strings.TrimSpace(value)
		subject = &trimmed
	}

	var organisationID *string
	switch value := payload["organisation_id"].(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			trimmed := strings.TrimSpace(value)
			organisationID = &trimmed
		}
	case float64:
		trimmed := fmt.Sprintf("%.0f", value)
		organisationID = &trimmed
	}

	dataContentType, _ := payload["datacontenttype"].(string)
	if strings.TrimSpace(dataContentType) == "" {
		dataContentType = DataContentTypeJSON
	}

	data := map[string]any{}
	if rawData, ok := payload["data"].(map[string]any); ok {
		data = rawData
	}

	eventTime := time.Now().UTC()
	switch value := payload["time"].(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, fmt.Errorf("invalid CloudEvent time: %w", err)
			}
			eventTime = parsed.UTC()
		}
	}

	return &Event{
		Type:            strings.TrimSpace(eventType),
		Source:          strings.TrimSpace(source),
		Subject:         subject,
		ID:              id,
		OrganisationID:  organisationID,
		Time:            eventTime,
		DataContentType: dataContentType,
		Data:            data,
	}, nil
}

func (e *Event) ToMap() map[string]any {
	result := map[string]any{
		"type":            e.Type,
		"source":          e.Source,
		"subject":         e.Subject,
		"id":              e.ID,
		"organisation_id": e.OrganisationID,
		"time":            e.Time.UTC().Format(time.RFC3339),
		"datacontenttype": e.DataContentType,
		"data":            e.Data,
	}
	return result
}

func (e *Event) ToJSON() (string, error) {
	raw, err := json.Marshal(e.ToMap())
	if err != nil {
		return "", fmt.Errorf("encode CloudEvent JSON: %w", err)
	}
	return string(raw), nil
}

func NewResponse(request *Event, responseType string, data map[string]any) *Event {
	merged := map[string]any{}
	for key, value := range request.Data {
		merged[key] = value
	}
	for key, value := range data {
		merged[key] = value
	}

	return &Event{
		Type:            responseType,
		Source:          "/ai-gateway",
		Subject:         &request.ID,
		ID:              generateID(),
		OrganisationID:  request.OrganisationID,
		Time:            time.Now().UTC(),
		DataContentType: DataContentTypeJSON,
		Data:            merged,
	}
}

func NewID() string {
	return generateID()
}

func generateID() string {
	now := time.Now().UTC().UnixNano()
	return fmt.Sprintf("%016x-%08x", now, now&0xffffffff)
}
