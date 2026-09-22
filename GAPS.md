# Gaps

A human must set the Discord application credentials, guild id, Customer role id, and bot invite with Manage Roles and Guild Members.

- Discord application credentials
- guild id
- Customer role id
- bot invite with Manage Roles and Guild Members

The process exits 1 until every name below is set. It prints each missing name.

- DISCORD_BOT_TOKEN
- DISCORD_APPLICATION_ID
- DISCORD_GUILD_ID
- DISCORD_CUSTOMER_ROLE_ID
- GENOS_API_ORIGIN
- GENOS_DISCORD_BOT_SECRET

The bot does not hold Kubernetes credentials, a kubeconfig, or a cluster token. Create secret `genos-discord` out of band with the keys above. Do not commit secret values.
