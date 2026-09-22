package bot

import (
	"fmt"
	"strings"

	"github.com/AnthonyPoschen/genos-discord/internal/genos"
)

const (
	// MessageFlagEphemeral is Discord interaction flag 64 (1<<6).
	MessageFlagEphemeral = 64
	// ApproveButtonLabel is the label on the confirmation button.
	ApproveButtonLabel = "Approve"
	maxConsoleRunes    = 1024
)

// RoleAction is the Customer role change for a fleet list.
type RoleAction string

const (
	// RoleGrant adds the Customer role.
	RoleGrant RoleAction = "grant"
	// RoleRevoke removes the Customer role.
	RoleRevoke RoleAction = "revoke"
)

// CustomerRoleAction grants the role only when the fleet list has at least one
// current server. Zero revokes it. Signup and deleted servers do not count;
// Genos returns current servers only, so the list length is the rule.
func CustomerRoleAction(serverCount int) RoleAction {
	if serverCount >= 1 {
		return RoleGrant
	}
	return RoleRevoke
}

// ResolveStatus is why a server option did or did not resolve.
type ResolveStatus int

const (
	// ResolveOK means one server was selected.
	ResolveOK ResolveStatus = iota
	// ResolveNone means the fleet is empty.
	ResolveNone
	// ResolveChoose means several servers exist and the option was omitted.
	ResolveChoose
	// ResolveMissing means the option is not in the fleet.
	ResolveMissing
)

// ResolveServer uses the only server when the option is omitted.
// Several servers require an id. The id may be passed directly.
func ResolveServer(servers []genos.Server, serverID string) (genos.Server, ResolveStatus) {
	usable := make([]genos.Server, 0, len(servers))
	for _, server := range servers {
		if strings.TrimSpace(server.ID) != "" {
			usable = append(usable, server)
		}
	}
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		switch len(usable) {
		case 0:
			return genos.Server{}, ResolveNone
		case 1:
			return usable[0], ResolveOK
		default:
			return genos.Server{}, ResolveChoose
		}
	}
	for _, server := range usable {
		if server.ID == serverID {
			return server, ResolveOK
		}
	}
	return genos.Server{}, ResolveMissing
}

// DecisionInput is a pure command decision. It does not perform I/O.
type DecisionInput struct {
	Command  string
	InGuild  bool
	Servers  []genos.Server
	ServerID string
	Text     string
}

// Decision is what to tell the user and whether Genos may be called now.
// An approval custom id means do not call the action or console endpoint.
type Decision struct {
	Content                    string
	Ephemeral                  bool
	Action                     string
	ActionServerID             string
	ConfirmUnsavedProgressLoss bool
	ConsoleText                string
	ConsoleServerID            string
	ApprovalCustomID           string
	ApprovalLabel              string
}

// Approval is a button click that may call Genos.
type Approval struct {
	Action                     string
	ServerID                   string
	ConfirmUnsavedProgressLoss bool
}

// Decide applies confirm rules before any Genos action call.
// start and a quiet restart run immediately. stop, force-stop, and a restart
// with players or notable updates wait for Approve. force-stop's message
// contains "unsaved progress". console is immediate.
func Decide(in DecisionInput) Decision {
	command := strings.ToLower(strings.TrimSpace(in.Command))
	out := Decision{Ephemeral: in.InGuild}
	switch command {
	case "servers":
		out.Content = formatServerList(in.Servers)
		return out
	case "status", "start", "stop", "force-stop", "restart", "console":
	default:
		out.Content = "Unknown command."
		return out
	}
	server, status := ResolveServer(in.Servers, in.ServerID)
	if status != ResolveOK {
		out.Content = resolveMessage(status)
		return out
	}
	name := displayName(server)
	switch command {
	case "status":
		out.Content = formatStatus(server)
	case "start":
		out.Action = "start"
		out.ActionServerID = server.ID
		out.Content = fmt.Sprintf("Start requested for %s.", name)
	case "stop":
		out.ApprovalCustomID = ApprovalCustomID("stop", server.ID)
		out.ApprovalLabel = ApproveButtonLabel
		out.Content = fmt.Sprintf("Approve to stop %s.", name)
	case "force-stop":
		out.ApprovalCustomID = ApprovalCustomID("force-stop", server.ID)
		out.ApprovalLabel = ApproveButtonLabel
		out.Content = fmt.Sprintf("Approve to force-stop %s. This can lose unsaved progress.", name)
	case "restart":
		if RestartNeedsApproval(server) {
			out.ApprovalCustomID = ApprovalCustomID("restart", server.ID)
			out.ApprovalLabel = ApproveButtonLabel
			out.Content = restartApprovalMessage(server)
			return out
		}
		out.Action = "restart"
		out.ActionServerID = server.ID
		out.Content = fmt.Sprintf("Restart requested for %s.", name)
	case "console":
		text := strings.TrimSpace(in.Text)
		switch {
		case text == "":
			out.Content = "Console text is required."
		case len([]rune(text)) > maxConsoleRunes:
			out.Content = "Console text is too long."
		default:
			out.ConsoleText = text
			out.ConsoleServerID = server.ID
			out.Content = fmt.Sprintf("Sent console command to %s.", name)
		}
	}
	return out
}

// RestartNeedsApproval is true when players are online or notable updates are pending.
func RestartNeedsApproval(server genos.Server) bool {
	return server.PlayerCount() > 0 || len(server.Notable()) > 0
}

// DecideApproval parses an Approve button. force-stop confirms unsaved progress.
// stop and restart do not. Unknown ids do not call Genos.
func DecideApproval(customID string) (Approval, bool) {
	parts := strings.SplitN(strings.TrimSpace(customID), ":", 3)
	if len(parts) != 3 || parts[0] != "approve" || strings.TrimSpace(parts[2]) == "" {
		return Approval{}, false
	}
	switch parts[1] {
	case "stop", "restart":
		return Approval{Action: parts[1], ServerID: parts[2]}, true
	case "force-stop":
		return Approval{Action: "force-stop", ServerID: parts[2], ConfirmUnsavedProgressLoss: true}, true
	default:
		return Approval{}, false
	}
}

// ApprovalCustomID is the button id the HTTP handler and the gateway share.
func ApprovalCustomID(action, serverID string) string {
	return "approve:" + action + ":" + serverID
}

// Choice is one autocomplete row. Value is the server id.
type Choice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AutocompleteChoices filters the fleet. At most 25 choices are returned.
func AutocompleteChoices(servers []genos.Server, query string) []Choice {
	query = strings.ToLower(strings.TrimSpace(query))
	choices := make([]Choice, 0, len(servers))
	seen := map[string]int{}
	for _, server := range servers {
		if strings.TrimSpace(server.ID) == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(server.Name), query) && !strings.Contains(strings.ToLower(server.ID), query) {
			continue
		}
		name := displayName(server)
		seen[name]++
		if seen[name] > 1 {
			name = displayName(server) + " (" + server.ID + ")"
		}
		choices = append(choices, Choice{Name: truncate(name, 100), Value: server.ID})
		if len(choices) == 25 {
			break
		}
	}
	return choices
}

func resolveMessage(status ResolveStatus) string {
	switch status {
	case ResolveNone:
		return "You have no servers."
	case ResolveChoose:
		return "Choose a server. You have more than one."
	default:
		return "That server was not found in your fleet."
	}
}

func formatServerList(servers []genos.Server) string {
	if len(servers) == 0 {
		return "You have no servers."
	}
	var b strings.Builder
	b.WriteString("Your servers:")
	for _, server := range servers {
		if strings.TrimSpace(server.ID) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n- %s (%s) - %s - %s - %d players", displayName(server), server.ID, gameName(server), statusName(server), server.PlayerCount())
	}
	return b.String()
}

func formatStatus(server genos.Server) string {
	notable := server.Notable()
	notableText := "none"
	if len(notable) > 0 {
		notableText = strings.Join(notable, ", ")
	}
	return fmt.Sprintf("%s (%s)\nGame: %s\nStatus: %s\nPlayers: %d\nNotable updates: %s",
		displayName(server), server.ID, gameName(server), statusName(server), server.PlayerCount(), notableText)
}

func restartApprovalMessage(server genos.Server) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Approve to restart %s.", displayName(server))
	if players := server.PlayerCount(); players > 0 {
		fmt.Fprintf(&b, " Players online: %d.", players)
	}
	if notable := server.Notable(); len(notable) > 0 {
		fmt.Fprintf(&b, " Notable updates: %s.", strings.Join(notable, ", "))
	}
	return b.String()
}

func displayName(server genos.Server) string {
	if name := strings.TrimSpace(server.Name); name != "" {
		return name
	}
	return server.ID
}

func gameName(server genos.Server) string {
	if name := strings.TrimSpace(server.Game.Name); name != "" {
		return name
	}
	return "unknown"
}

func statusName(server genos.Server) string {
	if status := strings.TrimSpace(server.Status); status != "" {
		return status
	}
	return "unknown"
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func withDetail(servers []genos.Server, detail genos.Server) []genos.Server {
	out := make([]genos.Server, len(servers))
	copy(out, servers)
	for i := range out {
		if out[i].ID == detail.ID {
			out[i] = detail
			return out
		}
	}
	return out
}
