package gateway_test

import (
	"testing"

	"github.com/AnthonyPoschen/genos-discord/internal/bot"
	"github.com/AnthonyPoschen/genos-discord/internal/gateway"
	"github.com/bwmarrin/discordgo"
)

func TestEphemeralFlagIsDiscord64(t *testing.T) {
	if discordgo.MessageFlagsEphemeral != bot.MessageFlagEphemeral || bot.MessageFlagEphemeral != 64 {
		t.Fatalf("ephemeral flag = %d", discordgo.MessageFlagsEphemeral)
	}
}

func TestGuildCommandsAreNotRoleGated(t *testing.T) {
	want := []string{"servers", "status", "start", "stop", "force-stop", "restart", "console"}
	got := gateway.Commands()
	if len(got) != len(want) {
		t.Fatalf("commands = %d", len(got))
	}
	for i, cmd := range got {
		if cmd.Name != want[i] {
			t.Fatalf("command %d = %s", i, cmd.Name)
		}
		if cmd.Description == "" || len(cmd.Description) > 100 {
			t.Fatalf("description %q", cmd.Description)
		}
		if cmd.DefaultMemberPermissions != nil {
			t.Fatalf("%s sets default member permissions", cmd.Name)
		}
		if cmd.GuildID != "" {
			t.Fatal("guild id is supplied at registration, not baked into the command")
		}
		for _, opt := range cmd.Options {
			if len(opt.Description) > 100 {
				t.Fatalf("option description %q", opt.Description)
			}
			if opt.Name == "server" && (opt.Required || !opt.Autocomplete || opt.Type != discordgo.ApplicationCommandOptionString) {
				t.Fatalf("server option = %#v", opt)
			}
		}
	}
}

func TestRequestFromInteractionWithoutGateway(t *testing.T) {
	command := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "guild-1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "user-1"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "console",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "server", Type: discordgo.ApplicationCommandOptionString, Value: "srv-9"},
				{Name: "text", Type: discordgo.ApplicationCommandOptionString, Value: "say hi"},
			},
		},
	}}
	req, err := gateway.RequestFromInteraction(command)
	if err != nil {
		t.Fatal(err)
	}
	if req.Kind != bot.KindCommand || req.Command != "console" || req.ServerID != "srv-9" || req.Text != "say hi" || req.DiscordUserID != "user-1" || req.GuildID != "guild-1" {
		t.Fatalf("command request = %+v", req)
	}

	dm := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		User: &discordgo.User{ID: "user-dm"},
		Data: discordgo.ApplicationCommandInteractionData{Name: "servers"},
	}}
	req, err = gateway.RequestFromInteraction(dm)
	if err != nil {
		t.Fatal(err)
	}
	if req.GuildID != "" || req.DiscordUserID != "user-dm" || req.Command != "servers" {
		t.Fatalf("dm request = %+v", req)
	}

	focused := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommandAutocomplete,
		User: &discordgo.User{ID: "user-1"},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "start",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "server", Type: discordgo.ApplicationCommandOptionString, Value: "al", Focused: true},
			},
		},
	}}
	req, err = gateway.RequestFromInteraction(focused)
	if err != nil {
		t.Fatal(err)
	}
	if req.Kind != bot.KindAutocomplete || req.FocusedOption != "server" || req.Focused != "al" {
		t.Fatalf("autocomplete = %+v", req)
	}

	button := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionMessageComponent,
		GuildID: "guild-1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "user-1"}},
		Data:    discordgo.MessageComponentInteractionData{CustomID: "approve:force-stop:srv-1", ComponentType: discordgo.ButtonComponent},
	}}
	req, err = gateway.RequestFromInteraction(button)
	if err != nil {
		t.Fatal(err)
	}
	if req.Kind != bot.KindApprove || req.CustomID != "approve:force-stop:srv-1" {
		t.Fatalf("button = %+v", req)
	}
}
