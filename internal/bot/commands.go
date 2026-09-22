package bot

// SlashOption is one guild slash-command option.
type SlashOption struct {
	Name         string
	Description  string
	Required     bool
	Autocomplete bool
}

// SlashCommand is a guild command registered for this install.
type SlashCommand struct {
	Name        string
	Description string
	Options     []SlashOption
}

// SlashCommands are guild-only. The server option is optional so a one-server
// fleet can omit it. Text is optional in the Discord payload because Discord
// rejects a required option placed after an optional one; Decide still requires text.
func SlashCommands() []SlashCommand {
	server := SlashOption{
		Name:         "server",
		Description:  "Server. Omit this when you only have one.",
		Autocomplete: true,
	}
	return []SlashCommand{
		{Name: "servers", Description: "List your Genos servers"},
		{Name: "status", Description: "Show the status of a server", Options: []SlashOption{server}},
		{Name: "start", Description: "Start a server", Options: []SlashOption{server}},
		{Name: "stop", Description: "Stop a server after you approve", Options: []SlashOption{server}},
		{Name: "force-stop", Description: "Force-stop a server after you approve", Options: []SlashOption{server}},
		{Name: "restart", Description: "Restart a server", Options: []SlashOption{server}},
		{Name: "console", Description: "Send text to the server console", Options: []SlashOption{
			server,
			{Name: "text", Description: "Console command text"},
		}},
	}
}
