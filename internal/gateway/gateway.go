// Package gateway adapts Discord gateway events onto bot.Service.
// Tests can translate interactions without opening a websocket.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AnthonyPoschen/genos-discord/internal/bot"
	"github.com/bwmarrin/discordgo"
)

// Run connects the gateway and registers this install's guild slash commands.
func Run(ctx context.Context, token, applicationID, guildID string, svc *bot.Service) error {
	if svc == nil {
		return errors.New("nil service")
	}
	session, err := discordgo.New(botAuthorization(token))
	if err != nil {
		return fmt.Errorf("discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentDirectMessages
	session.StateEnabled = false
	session.LogLevel = discordgo.LogError
	session.AddHandler(func(s *discordgo.Session, event *discordgo.InteractionCreate) {
		onInteraction(s, svc, event)
	})
	if err := session.Open(); err != nil {
		return fmt.Errorf("discord gateway: %w", err)
	}
	defer session.Close()
	if _, err := session.ApplicationCommandBulkOverwrite(applicationID, guildID, Commands()); err != nil {
		return fmt.Errorf("register guild commands: %w", err)
	}
	slog.Info("discord gateway connected", "guild", guildID)
	<-ctx.Done()
	return nil
}

// NotifyContext returns a context cancelled by interrupt or SIGTERM.
func NotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Commands are the guild slash commands sent to Discord.
func Commands() []*discordgo.ApplicationCommand {
	specs := bot.SlashCommands()
	out := make([]*discordgo.ApplicationCommand, 0, len(specs))
	for _, spec := range specs {
		cmd := &discordgo.ApplicationCommand{
			Name:        spec.Name,
			Description: spec.Description,
			Type:        discordgo.ChatApplicationCommand,
		}
		if len(spec.Options) > 0 {
			cmd.Options = make([]*discordgo.ApplicationCommandOption, 0, len(spec.Options))
			for _, opt := range spec.Options {
				cmd.Options = append(cmd.Options, &discordgo.ApplicationCommandOption{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         opt.Name,
					Description:  opt.Description,
					Required:     opt.Required,
					Autocomplete: opt.Autocomplete,
				})
			}
		}
		out = append(out, cmd)
	}
	return out
}

// RequestFromInteraction maps a gateway event onto the decision handler.
func RequestFromInteraction(event *discordgo.InteractionCreate) (bot.Request, error) {
	if event == nil || event.Interaction == nil {
		return bot.Request{}, errors.New("missing interaction")
	}
	interaction := event.Interaction
	req := bot.Request{
		DiscordUserID: discordUserID(interaction),
		GuildID:       interaction.GuildID,
	}
	switch interaction.Type {
	case discordgo.InteractionApplicationCommand:
		data := interaction.ApplicationCommandData()
		req.Kind = bot.KindCommand
		req.Command = data.Name
		fillOptions(&req, data.Options)
	case discordgo.InteractionApplicationCommandAutocomplete:
		data := interaction.ApplicationCommandData()
		req.Kind = bot.KindAutocomplete
		req.Command = data.Name
		fillOptions(&req, data.Options)
	case discordgo.InteractionMessageComponent:
		data := interaction.MessageComponentData()
		req.Kind = bot.KindApprove
		req.CustomID = data.CustomID
	default:
		return bot.Request{}, fmt.Errorf("unsupported interaction type %s", interaction.Type)
	}
	return req, nil
}

func onInteraction(session *discordgo.Session, svc *bot.Service, event *discordgo.InteractionCreate) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("discord interaction panicked", "err", rec)
		}
	}()
	if session == nil || event == nil || event.Interaction == nil {
		return
	}
	if event.Type == discordgo.InteractionPing {
		if err := session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponsePong}); err != nil {
			slog.Error("discord pong failed", "err", err)
		}
		return
	}
	req, err := RequestFromInteraction(event)
	if err != nil {
		slog.Warn("discord interaction ignored", "err", err)
		return
	}
	if event.Type == discordgo.InteractionApplicationCommandAutocomplete {
		resp := svc.Handle(context.Background(), req)
		if err := session.InteractionRespond(event.Interaction, autocompleteResponse(resp)); err != nil {
			slog.Error("discord autocomplete failed", "err", err)
		}
		return
	}
	flags := discordgo.MessageFlags(0)
	if event.GuildID != "" {
		flags = discordgo.MessageFlagsEphemeral
	}
	if err := session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	}); err != nil {
		slog.Error("discord defer failed", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp := svc.Handle(ctx, req)
	params := &discordgo.WebhookParams{
		Content: resp.Content,
		Flags:   discordgo.MessageFlags(resp.Flags),
	}
	if resp.Approve != nil {
		params.Components = approveComponents(resp.Approve)
	}
	if _, err := session.FollowupMessageCreate(event.Interaction, true, params); err != nil {
		slog.Error("discord followup failed", "err", err)
	}
}

func autocompleteResponse(resp bot.Response) *discordgo.InteractionResponse {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(resp.Choices))
	for _, choice := range resp.Choices {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  choice.Name,
			Value: choice.Value,
		})
	}
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}
}

func approveComponents(button *bot.ApproveButton) []discordgo.MessageComponent {
	label := button.Label
	if label == "" {
		label = bot.ApproveButtonLabel
	}
	style := discordgo.PrimaryButton
	if strings.Contains(button.CustomID, "force-stop") {
		style = discordgo.DangerButton
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: label, Style: style, CustomID: button.CustomID},
		}},
	}
}

func fillOptions(req *bot.Request, options []*discordgo.ApplicationCommandInteractionDataOption) {
	for _, opt := range options {
		value, ok := stringOption(opt)
		if !ok {
			continue
		}
		switch opt.Name {
		case "server":
			req.ServerID = value
		case "text":
			req.Text = value
		}
		if opt.Focused {
			req.FocusedOption = opt.Name
			req.Focused = value
		}
	}
}

func stringOption(opt *discordgo.ApplicationCommandInteractionDataOption) (string, bool) {
	if opt == nil || opt.Type != discordgo.ApplicationCommandOptionString {
		return "", false
	}
	value, ok := opt.Value.(string)
	return value, ok
}

func discordUserID(interaction *discordgo.Interaction) string {
	if interaction.Member != nil && interaction.Member.User != nil && interaction.Member.User.ID != "" {
		return interaction.Member.User.ID
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	return ""
}

func botAuthorization(token string) string {
	token = strings.TrimSpace(token)
	if len(token) >= 4 && strings.EqualFold(token[:4], "bot ") {
		return "Bot " + strings.TrimSpace(token[4:])
	}
	return "Bot " + token
}
