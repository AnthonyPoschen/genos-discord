package bot_test

import (
	"strings"
	"testing"

	"github.com/AnthonyPoschen/genos-discord/internal/bot"
	"github.com/AnthonyPoschen/genos-discord/internal/genos"
)

func TestCustomerRoleActionUsesFleetLength(t *testing.T) {
	if bot.CustomerRoleAction(0) != bot.RoleRevoke {
		t.Fatal("zero servers should revoke")
	}
	if bot.CustomerRoleAction(1) != bot.RoleGrant || bot.CustomerRoleAction(2) != bot.RoleGrant {
		t.Fatal("one or more current servers should grant")
	}
}

func TestDecideConfirmRules(t *testing.T) {
	one := decisionServer("srv-1", "Alpha", 0, nil)
	cases := []struct {
		name       string
		command    string
		players    int64
		notable    []string
		wantAction string
		wantPhrase string
	}{
		{name: "start ignores players and updates", command: "start", players: 4, notable: []string{"patch"}, wantAction: "start"},
		{name: "stop waits", command: "stop", wantPhrase: ""},
		{name: "force-stop waits and names unsaved progress", command: "force-stop", wantPhrase: "unsaved progress"},
		{name: "restart with players waits", command: "restart", players: 2},
		{name: "restart with notable update waits", command: "restart", notable: []string{"Factorio 2.1.13"}},
		{name: "quiet restart runs", command: "restart", wantAction: "restart"},
		{name: "blank notable update is quiet", command: "restart", notable: []string{"  "}, wantAction: "restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := decisionServer("srv-1", "Alpha", tc.players, tc.notable)
			got := bot.Decide(bot.DecisionInput{
				Command:  tc.command,
				InGuild:  true,
				Servers:  []genos.Server{server},
				ServerID: server.ID,
			})
			if !got.Ephemeral {
				t.Fatal("guild reply should be ephemeral")
			}
			if got.Action != "" && got.ApprovalCustomID != "" {
				t.Fatalf("decision both runs and waits: %+v", got)
			}
			if got.ConfirmUnsavedProgressLoss {
				t.Fatal("command decision must not confirm unsaved progress before Approve")
			}
			if got.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q (%+v)", got.Action, tc.wantAction, got)
			}
			if tc.wantAction == "" {
				if got.ApprovalCustomID == "" || got.ApprovalLabel != bot.ApproveButtonLabel {
					t.Fatalf("missing approve button: %+v", got)
				}
			} else if got.ApprovalCustomID != "" {
				t.Fatalf("unexpected approval: %+v", got)
			}
			if tc.wantPhrase != "" && !strings.Contains(got.Content, tc.wantPhrase) {
				t.Fatalf("content %q", got.Content)
			}
		})
	}
	if got := bot.Decide(bot.DecisionInput{Command: "stop", Servers: []genos.Server{one}}); got.Ephemeral {
		t.Fatal("DM stop should not be ephemeral")
	}
	console := bot.Decide(bot.DecisionInput{Command: "console", InGuild: true, Servers: []genos.Server{one}, Text: "say hi"})
	if console.ConsoleText != "say hi" || console.ApprovalCustomID != "" || console.Action != "" {
		t.Fatalf("console = %+v", console)
	}
}

func TestDecideServerSelection(t *testing.T) {
	alpha := decisionServer("srv-1", "Alpha", 0, nil)
	beta := decisionServer("srv-2", "Beta", 0, nil)
	omitted := bot.Decide(bot.DecisionInput{Command: "start", Servers: []genos.Server{alpha}})
	if omitted.Action != "start" || omitted.ActionServerID != "srv-1" {
		t.Fatalf("single server = %+v", omitted)
	}
	choose := bot.Decide(bot.DecisionInput{Command: "start", Servers: []genos.Server{alpha, beta}})
	if choose.Action != "" || !strings.Contains(choose.Content, "Choose a server") {
		t.Fatalf("several servers = %+v", choose)
	}
	direct := bot.Decide(bot.DecisionInput{Command: "start", Servers: []genos.Server{alpha, beta}, ServerID: "srv-2"})
	if direct.Action != "start" || direct.ActionServerID != "srv-2" {
		t.Fatalf("direct id = %+v", direct)
	}
	missing := bot.Decide(bot.DecisionInput{Command: "start", Servers: []genos.Server{alpha}, ServerID: "other"})
	if missing.Action != "" || !strings.Contains(missing.Content, "not found") {
		t.Fatalf("missing = %+v", missing)
	}
}

func TestDecideApprovalConfirmFlag(t *testing.T) {
	stop, ok := bot.DecideApproval("approve:stop:srv-1")
	if !ok || stop.Action != "stop" || stop.ServerID != "srv-1" || stop.ConfirmUnsavedProgressLoss {
		t.Fatalf("stop = %+v ok=%v", stop, ok)
	}
	restart, ok := bot.DecideApproval("approve:restart:srv-1")
	if !ok || restart.ConfirmUnsavedProgressLoss || restart.Action != "restart" {
		t.Fatalf("restart = %+v", restart)
	}
	force, ok := bot.DecideApproval("approve:force-stop:srv:with:colon")
	if !ok || force.Action != "force-stop" || force.ServerID != "srv:with:colon" || !force.ConfirmUnsavedProgressLoss {
		t.Fatalf("force-stop = %+v ok=%v", force, ok)
	}
	if _, ok := bot.DecideApproval("approve:start:srv-1"); ok {
		t.Fatal("start is not an approval action")
	}
	if _, ok := bot.DecideApproval("nope"); ok {
		t.Fatal("invalid approval accepted")
	}
}

func TestAutocompleteChoices(t *testing.T) {
	servers := []genos.Server{
		decisionServer("srv-1", "Alpha", 0, nil),
		decisionServer("srv-2", "Beta", 0, nil),
	}
	all := bot.AutocompleteChoices(servers, "")
	if len(all) != 2 || all[0].Value != "srv-1" {
		t.Fatalf("choices = %#v", all)
	}
	filtered := bot.AutocompleteChoices(servers, "bet")
	if len(filtered) != 1 || filtered[0].Value != "srv-2" {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func decisionServer(id, name string, players int64, notable []string) genos.Server {
	count := players
	return genos.Server{
		ID:             id,
		Name:           name,
		Status:         "Running",
		Game:           genos.Game{Name: "Factorio"},
		NotableUpdates: notable,
		Metrics:        genos.Metrics{PlayerCount: &count},
	}
}
