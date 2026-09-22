package config

import (
	"os"
	"strings"
)

// RequiredEnv is the process environment, in the order a missing-name report prints them.
var RequiredEnv = []string{
	"DISCORD_BOT_TOKEN",
	"DISCORD_APPLICATION_ID",
	"DISCORD_GUILD_ID",
	"DISCORD_CUSTOMER_ROLE_ID",
	"GENOS_API_ORIGIN",
	"GENOS_DISCORD_BOT_SECRET",
}

// HumanGap is printed when configuration is incomplete. A human has to create
// the Discord application, invite the bot, and choose the Customer role.
const HumanGap = "A human must set the Discord application credentials, guild id, Customer role id, and bot invite with Manage Roles and Guild Members."

// Config is everything the process needs. Tokens are never logged by this package.
type Config struct {
	DiscordBotToken       string
	DiscordApplicationID  string
	DiscordGuildID        string
	DiscordCustomerRoleID string
	GenosAPIOrigin        string
	GenosDiscordBotSecret string
}

// Load reads RequiredEnv. Empty and whitespace-only values are missing.
// getenv may be nil, in which case os.Getenv is used.
func Load(getenv func(string) string) (Config, []string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	vals := make(map[string]string, len(RequiredEnv))
	var missing []string
	for _, name := range RequiredEnv {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			missing = append(missing, name)
			continue
		}
		vals[name] = value
	}
	cfg := Config{
		DiscordBotToken:       vals["DISCORD_BOT_TOKEN"],
		DiscordApplicationID:  vals["DISCORD_APPLICATION_ID"],
		DiscordGuildID:        vals["DISCORD_GUILD_ID"],
		DiscordCustomerRoleID: vals["DISCORD_CUSTOMER_ROLE_ID"],
		GenosAPIOrigin:        vals["GENOS_API_ORIGIN"],
		GenosDiscordBotSecret: vals["GENOS_DISCORD_BOT_SECRET"],
	}
	return cfg, missing
}
