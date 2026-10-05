package services

import (
	"database/sql"
	"encoding/json"
	"strings"

	"middle-monitor/backend/models"
)

// routingRule is the minimal view of an alert rule used for notification routing.
type routingRule struct {
	channels   []int64
	tags       string
	targetType string
	targetID   *int64
}

// routingRulesForSeverity returns enabled rules that opted in to the given severity.
func routingRulesForSeverity(db *sql.DB, orgID int64, severity string) []routingRule {
	col := "notify_critical"
	if strings.EqualFold(severity, "warning") {
		col = "notify_warning"
	}
	rows, err := db.Query(
		`SELECT channels, COALESCE(tags, ''), COALESCE(target_type, 'any'), target_id
		 FROM alert_rules
		 WHERE organization_id = $1 AND enabled = true AND `+col+` = true`, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []routingRule
	for rows.Next() {
		var chJSON []byte
		var tags, targetType string
		var targetID sql.NullInt64
		if err := rows.Scan(&chJSON, &tags, &targetType, &targetID); err != nil {
			continue
		}
		var ids []int64
		if len(chJSON) > 0 {
			_ = json.Unmarshal(chJSON, &ids)
		}
		r := routingRule{channels: ids, tags: tags, targetType: targetType}
		if targetID.Valid {
			v := targetID.Int64
			r.targetID = &v
		}
		out = append(out, r)
	}
	return out
}

// ruleMatchesTarget returns true when the routing rule applies to the given service or host.
// A rule with target_type "any" (or empty) always matches.
func ruleMatchesTarget(r routingRule, serviceID, hostID *int64) bool {
	if r.targetType == "" || r.targetType == "any" {
		return true
	}
	if r.targetID == nil {
		return true // no specific target set → treat as global
	}
	if r.targetType == "service" && serviceID != nil && *r.targetID == *serviceID {
		return true
	}
	if r.targetType == "host" && hostID != nil && *r.targetID == *hostID {
		return true
	}
	return false
}

// RouteAlert delivers an alert to the channels of every enabled routing rule that
// includes the given severity, forwarding each rule's tags. With no matching rule
// it falls back to all enabled org channels (so alerts are never silently lost).
// serviceID and hostID narrow which rules apply; nil means global alert (all rules match).
func RouteAlert(db *sql.DB, orgID int64, severity, subject, body, alias string) {
	RouteAlertTargeted(db, orgID, severity, subject, body, alias, nil, nil)
}

// RouteAlertTargeted is like RouteAlert but respects per-rule target scoping.
func RouteAlertTargeted(db *sql.DB, orgID int64, severity, subject, body, alias string, serviceID, hostID *int64) {
	alertSvc := NewAlertService(db)
	allRules := routingRulesForSeverity(db, orgID, severity)

	// Filter to rules that match this specific service/host (or are global).
	var rules []routingRule
	for _, r := range allRules {
		if ruleMatchesTarget(r, serviceID, hostID) {
			rules = append(rules, r)
		}
	}

	if len(rules) == 0 {
		channels, _ := alertSvc.GetChannels(orgID)
		for _, ch := range channels {
			if ch.Enabled {
				_ = SendNotificationWithAlias(ch, subject, body, alias)
			}
		}
		return
	}

	sent := map[int64]bool{} // one send per channel even if several rules match
	for _, rule := range rules {
		tags := splitTags(rule.tags)
		channels := rule.resolveChannels(alertSvc, orgID)
		for _, ch := range channels {
			if !ch.Enabled || sent[ch.ID] {
				continue
			}
			sent[ch.ID] = true
			_ = SendNotificationWithAliasTags(ch, subject, body, alias, tags)
		}
	}
}

// RouteResolve closes/resolves the alert on every channel referenced by any
// enabled routing rule (union of warning + critical), or all org channels when
// no routing rules exist.
func RouteResolve(db *sql.DB, orgID int64, alias, subject, body string) {
	RouteResolveTargeted(db, orgID, alias, subject, body, nil, nil)
}

// RouteResolveTargeted is like RouteResolve but respects per-rule target scoping.
func RouteResolveTargeted(db *sql.DB, orgID int64, alias, subject, body string, serviceID, hostID *int64) {
	alertSvc := NewAlertService(db)
	allRules := append(routingRulesForSeverity(db, orgID, "warning"), routingRulesForSeverity(db, orgID, "critical")...)

	seen := map[int64]bool{}
	var target []models.NotificationChannel
	matchedRules := false
	for _, rule := range allRules {
		if !ruleMatchesTarget(rule, serviceID, hostID) {
			continue
		}
		matchedRules = true
		for _, ch := range rule.resolveChannels(alertSvc, orgID) {
			if !seen[ch.ID] {
				seen[ch.ID] = true
				target = append(target, ch)
			}
		}
	}
	if !matchedRules {
		target, _ = alertSvc.GetChannels(orgID)
	}
	for _, ch := range target {
		if ch.Enabled {
			_ = ResolveNotification(ch, alias, subject, body)
		}
	}
}

// RouteIncidentEvent delivers a typed incident transition to the same channels
// a resolve would reach. JSM has its own notion of closing an alert, so a
// resolution still goes through ResolveNotification there.
func RouteIncidentEvent(db *sql.DB, orgID int64, event NotificationEvent, serviceID, hostID *int64) {
	alertSvc := NewAlertService(db)
	allRules := append(routingRulesForSeverity(db, orgID, "warning"), routingRulesForSeverity(db, orgID, "critical")...)

	seen := map[int64]bool{}
	var target []models.NotificationChannel
	matchedRules := false
	for _, rule := range allRules {
		if !ruleMatchesTarget(rule, serviceID, hostID) {
			continue
		}
		matchedRules = true
		for _, ch := range rule.resolveChannels(alertSvc, orgID) {
			if !seen[ch.ID] {
				seen[ch.ID] = true
				target = append(target, ch)
			}
		}
	}
	if !matchedRules {
		target, _ = alertSvc.GetChannels(orgID)
	}

	for _, ch := range target {
		if !ch.Enabled || !channelWantsEvent(ch, event.Event) {
			continue
		}
		if ch.Type == "jsm" && event.Event == EventIncidentResolved {
			_ = ResolveNotification(ch, event.DedupKey, event.Title, event.Message)
			continue
		}
		_ = SendNotificationEvent(ch, event, nil)
	}
}

// channelWantsEvent keeps acknowledgements and reopenings on the webhook
// channels only. They exist so an automated receiver can follow the whole
// lifecycle; a human already sees an acknowledgement in the dashboard, and
// mailing it to them would be a new notification they never asked for.
// Openings, escalations and resolutions still reach every channel.
func channelWantsEvent(channel models.NotificationChannel, event string) bool {
	switch event {
	case EventIncidentAcknowledged, EventIncidentReopened:
		return channel.Type == "webhook"
	default:
		return true
	}
}

func (r routingRule) resolveChannels(alertSvc *AlertService, orgID int64) []models.NotificationChannel {
	if len(r.channels) == 0 {
		channels, _ := alertSvc.GetChannels(orgID)
		return channels
	}
	channels, _ := alertSvc.GetChannelsByIDs(r.channels, orgID)
	return channels
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
