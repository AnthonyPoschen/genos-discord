package bot

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AnthonyPoschen/genos-discord/internal/discordapi"
	"github.com/AnthonyPoschen/genos-discord/internal/genos"
)

const (
	// KindCommand is a submitted slash command.
	KindCommand = "command"
	// KindApprove is an Approve button click.
	KindApprove = "approve"
	// KindAutocomplete is a server-option autocomplete interaction.
	KindAutocomplete = "autocomplete"
)

// FleetAPI is the Genos customer API used by command decisions.
type FleetAPI interface {
	ListServers(ctx context.Context, discordUserID string) ([]genos.Server, error)
	GetServer(ctx context.Context, discordUserID, serverID string) (genos.Server, error)
	RequestAction(ctx context.Context, discordUserID, serverID, actionType string, confirmUnsavedProgressLoss bool) error
	SendConsole(ctx context.Context, discordUserID, serverID, text string) error
}

// RoleAPI adds or removes the Customer role. It does not read current membership.
type RoleAPI interface {
	GrantCustomer(ctx context.Context, discordUserID string) error
	RevokeCustomer(ctx context.Context, discordUserID string) error
}

// Options configures the HTTP clients used for Genos and Discord role updates.
type Options struct {
	HTTP            *http.Client
	GenosOrigin     string
	GenosBotSecret  string
	DiscordAPIBase  string
	DiscordBotToken string
	GuildID         string
	CustomerRoleID  string
	Fleet           FleetAPI
	Roles           RoleAPI
}

// Request is the gateway translation and the HTTP handler body.
type Request struct {
	Kind          string `json:"kind"`
	Command       string `json:"command"`
	DiscordUserID string `json:"discordUserID"`
	ServerID      string `json:"serverID"`
	Text          string `json:"text"`
	GuildID       string `json:"guildID"`
	CustomID      string `json:"customID"`
	FocusedOption string `json:"focusedOption"`
	Focused       string `json:"focused"`
}

// ApproveButton is rendered as a Discord button by the gateway.
type ApproveButton struct {
	CustomID string `json:"customID"`
	Label    string `json:"label"`
}

// Response is returned by the HTTP handler and by Handle.
type Response struct {
	Content      string         `json:"content,omitempty"`
	Flags        int            `json:"flags,omitempty"`
	Approve      *ApproveButton `json:"approve,omitempty"`
	Choices      []Choice       `json:"choices,omitempty"`
	Autocomplete bool           `json:"autocomplete,omitempty"`
}

// Service decides commands and performs Genos and Discord role calls.
type Service struct {
	fleet   FleetAPI
	roles   RoleAPI
	guildID string
}

var (
	_ FleetAPI = (*genos.Client)(nil)
	_ RoleAPI  = (*discordapi.Client)(nil)
)

// New builds a service. HTTP clients are used unless Fleet or Roles is set.
func New(opts Options) *Service {
	fleet := opts.Fleet
	if fleet == nil {
		fleet = genos.NewClient(opts.GenosOrigin, opts.GenosBotSecret, opts.HTTP)
	}
	roles := opts.Roles
	if roles == nil {
		roles = discordapi.NewClient(opts.DiscordAPIBase, opts.DiscordBotToken, opts.GuildID, opts.CustomerRoleID, opts.HTTP)
	}
	return &Service{
		fleet:   fleet,
		roles:   roles,
		guildID: strings.TrimSpace(opts.GuildID),
	}
}

// ObserveLink syncs the Customer role from the current fleet.
// Call it when a Discord account link is observed.
func (s *Service) ObserveLink(ctx context.Context, discordUserID string) error {
	if strings.TrimSpace(discordUserID) == "" {
		return errors.New("discord user id is empty")
	}
	_, err := s.syncFleet(ctx, discordUserID)
	return err
}

// Handle runs one command, approval, or autocomplete.
// Confirm rules are applied before RequestAction. Role membership is not read.
func (s *Service) Handle(ctx context.Context, req Request) Response {
	req.GuildID = strings.TrimSpace(req.GuildID)
	req.DiscordUserID = strings.TrimSpace(req.DiscordUserID)
	req.ServerID = strings.TrimSpace(req.ServerID)
	req.CustomID = strings.TrimSpace(req.CustomID)
	kind := normalizeKind(req)
	flags := s.flags(req.GuildID)
	if req.GuildID != "" && req.GuildID != s.guildID {
		if kind == KindAutocomplete {
			return Response{Autocomplete: true, Choices: []Choice{}}
		}
		return Response{Content: "This install only serves its configured guild.", Flags: MessageFlagEphemeral}
	}
	if strings.TrimSpace(req.DiscordUserID) == "" {
		if kind == KindAutocomplete {
			return Response{Autocomplete: true, Choices: []Choice{}}
		}
		return Response{Content: "Discord did not identify you.", Flags: flags}
	}
	switch kind {
	case KindAutocomplete:
		return s.autocomplete(ctx, req)
	case KindApprove:
		return s.handleApprove(ctx, req, flags)
	default:
		return s.handleCommand(ctx, req, flags)
	}
}

// ServeHTTP is the test and adapter entry point. It does not open a gateway socket.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.Handle(r.Context(), req)); err != nil {
		slog.Warn("encode interaction response", "err", err)
	}
}

func (s *Service) handleCommand(ctx context.Context, req Request, flags int) Response {
	servers, err := s.syncFleet(ctx, req.DiscordUserID)
	if err != nil {
		slog.Warn("genos fleet failed", "err", err)
		return Response{Content: "Genos could not list your servers.", Flags: flags}
	}
	command := strings.ToLower(strings.TrimSpace(req.Command))
	if command == "restart" || command == "status" {
		refreshed, failed := s.refreshServer(ctx, req, servers, flags)
		if failed != nil {
			return *failed
		}
		servers = refreshed
	}
	decision := Decide(DecisionInput{
		Command:  command,
		InGuild:  req.GuildID != "",
		Servers:  servers,
		ServerID: req.ServerID,
		Text:     req.Text,
	})
	return s.apply(ctx, req, flags, decision)
}

func (s *Service) apply(ctx context.Context, req Request, flags int, decision Decision) Response {
	// Approval is returned before any lifecycle or console call.
	if decision.ApprovalCustomID != "" {
		if decision.Action != "" || decision.ConsoleText != "" {
			return Response{Content: "Refusing to run an action that still needs approval.", Flags: flags}
		}
		label := decision.ApprovalLabel
		if label == "" {
			label = ApproveButtonLabel
		}
		return Response{
			Content: decision.Content,
			Flags:   flags,
			Approve: &ApproveButton{CustomID: decision.ApprovalCustomID, Label: label},
		}
	}
	if decision.Action == "force-stop" && !decision.ConfirmUnsavedProgressLoss {
		return Response{Content: "Refusing to force-stop without unsaved-progress confirmation.", Flags: flags}
	}
	if decision.Action != "" {
		if err := s.fleet.RequestAction(ctx, req.DiscordUserID, decision.ActionServerID, decision.Action, decision.ConfirmUnsavedProgressLoss); err != nil {
			slog.Warn("genos action failed", "action", decision.Action, "err", err)
			return Response{Content: "Genos rejected that action.", Flags: flags}
		}
	}
	if decision.ConsoleText != "" {
		if err := s.fleet.SendConsole(ctx, req.DiscordUserID, decision.ConsoleServerID, decision.ConsoleText); err != nil {
			slog.Warn("genos console failed", "err", err)
			if errors.Is(err, genos.ErrNoInteractiveChannel) {
				return Response{Content: "No interactive console channel is available.", Flags: flags}
			}
			return Response{Content: "Genos rejected that console command.", Flags: flags}
		}
	}
	return Response{Content: decision.Content, Flags: flags}
}

func (s *Service) handleApprove(ctx context.Context, req Request, flags int) Response {
	approval, ok := DecideApproval(req.CustomID)
	if !ok {
		return Response{Content: "That approval is not valid.", Flags: flags}
	}
	servers, err := s.syncFleet(ctx, req.DiscordUserID)
	if err != nil {
		slog.Warn("genos fleet failed", "err", err)
		return Response{Content: "Genos could not list your servers.", Flags: flags}
	}
	var name string
	found := false
	for _, server := range servers {
		if server.ID == approval.ServerID {
			found = true
			name = displayName(server)
			break
		}
	}
	if !found {
		return Response{Content: "That server was not found in your fleet.", Flags: flags}
	}
	if err := s.fleet.RequestAction(ctx, req.DiscordUserID, approval.ServerID, approval.Action, approval.ConfirmUnsavedProgressLoss); err != nil {
		slog.Warn("genos action failed", "action", approval.Action, "err", err)
		return Response{Content: "Genos rejected that action.", Flags: flags}
	}
	return Response{Content: approvalVerb(approval.Action) + " requested for " + name + ".", Flags: flags}
}

func (s *Service) autocomplete(ctx context.Context, req Request) Response {
	if req.FocusedOption != "" && req.FocusedOption != "server" {
		return Response{Autocomplete: true, Choices: []Choice{}}
	}
	servers, err := s.syncFleet(ctx, req.DiscordUserID)
	if err != nil {
		slog.Warn("autocomplete fleet failed", "err", err)
		return Response{Autocomplete: true, Choices: []Choice{}}
	}
	return Response{Autocomplete: true, Choices: AutocompleteChoices(servers, req.Focused)}
}

func (s *Service) refreshServer(ctx context.Context, req Request, servers []genos.Server, flags int) ([]genos.Server, *Response) {
	server, status := ResolveServer(servers, req.ServerID)
	if status != ResolveOK {
		return servers, nil
	}
	detail, err := s.fleet.GetServer(ctx, req.DiscordUserID, server.ID)
	if err != nil {
		slog.Warn("genos server detail failed", "server", server.ID, "err", err)
		resp := Response{Content: "Genos could not load that server.", Flags: flags}
		return nil, &resp
	}
	return withDetail(servers, detail), nil
}

// syncFleet lists current servers and applies the Customer role from that length.
// A list error does not remove the role. A role error does not block the command.
func (s *Service) syncFleet(ctx context.Context, discordUserID string) ([]genos.Server, error) {
	servers, err := s.fleet.ListServers(ctx, discordUserID)
	if err != nil {
		return nil, err
	}
	if err := s.applyRole(ctx, discordUserID, len(servers)); err != nil {
		slog.Warn("customer role sync failed", "user", discordUserID, "owned", len(servers), "err", err)
	}
	return servers, nil
}

func (s *Service) applyRole(ctx context.Context, discordUserID string, serverCount int) error {
	switch CustomerRoleAction(serverCount) {
	case RoleGrant:
		return s.roles.GrantCustomer(ctx, discordUserID)
	default:
		return s.roles.RevokeCustomer(ctx, discordUserID)
	}
}

func (s *Service) flags(guildID string) int {
	if guildID != "" {
		return MessageFlagEphemeral
	}
	return 0
}

func normalizeKind(req Request) string {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case KindCommand, KindApprove, KindAutocomplete:
		return kind
	}
	if strings.HasPrefix(strings.TrimSpace(req.CustomID), "approve:") {
		return KindApprove
	}
	return KindCommand
}

func approvalVerb(action string) string {
	switch action {
	case "stop":
		return "Stop"
	case "restart":
		return "Restart"
	case "force-stop":
		return "Force-stop"
	case "start":
		return "Start"
	default:
		return "Action"
	}
}
