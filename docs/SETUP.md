# TrendPilot setup guide

The setup wizard asks for four keys. This guide shows where to find each one, click by click.
**You don't need to know how to code.** Everything here is free.

> 💡 **What's a "key"?** It's a password that lets TrendPilot read public data from YouTube, Twitch and so on, on your behalf. Keep your keys private, the same as your passwords.

**Contents**

1. [Start the wizard](#start-the-wizard)
2. [Discord bot token](#step-1-discord-bot-token): how you talk to TrendPilot
3. [YouTube key](#step-2-youtube-key): YouTube trends and video tracking
4. [Twitch app](#step-3-twitch-app): which games are trending, best times to stream
5. [AI writer](#step-4-ai-writer): caption and hashtag ideas
6. [Region and time zone](#step-5-region-and-time-zone)
7. [Add the bot to your server](#last-step-add-the-bot-to-your-server)
8. [Troubleshooting](#troubleshooting)

You can skip any step by pressing **Enter**. TrendPilot works with whatever you set up. To add or change keys later, double-click
**Change settings** in the TrendPilot folder.

---

## Start the wizard

1. Download TrendPilot from the [Releases page](https://github.com/Mathiew1618/trendpilot/releases/latest) and unzip it into a folder you'll remember, for example `Documents\TrendPilot`.
2. Double-click **trendpilot** (**trendpilot.exe** on Windows).
3. A black window opens with the setup steps. Keep this guide open next to it.

> **To paste into the window:** on Windows, right-click inside the window. On Mac, press Cmd+V.

---

## Step 1: Discord bot token

This creates *your own* TrendPilot bot account.

1. Go to **<https://discord.com/developers/applications>** and log in with your normal Discord account.
2. Click **New Application** (top right), type `TrendPilot`, tick the box, click **Create**.
3. In the left menu click **Bot**.
4. Click **Reset Token** → **Yes, do it!** (Discord may ask for your password or 2FA code).
5. Click **Copy**, then paste it into the TrendPilot window and press Enter.

✅ You should see `Connected as TrendPilot`.

> You don't need to turn on any "Privileged Gateway Intents". TrendPilot doesn't read your server's messages.

---

## Step 2: YouTube key

1. Go to **<https://console.cloud.google.com/>** and log in with any Google account.
2. If asked, agree to the terms. Click **Select a project** (top left) → **New Project**, name it `TrendPilot`, and click **Create**. Make sure the new project is selected.
3. In the search bar at the top, type **YouTube Data API v3**, click it, then click **Enable**.
4. Click **Credentials** in the left menu → **+ Create credentials** → **API key**.
5. Copy the key and paste it into TrendPilot.

✅ You should see `YouTube key works`. If it says the key doesn't work, wait one minute and try again, because new keys take a moment to switch on.

> **Optional but recommended:** click the key's name → under *API restrictions* choose **Restrict key** → tick **YouTube Data API v3** → **Save**. Then the key can't be misused for anything else.
>
> **No credit card is needed.** The free allowance is 10,000 requests a day, and TrendPilot uses about 600.

---

## Step 3: Twitch app

1. Go to **<https://dev.twitch.tv/console/apps>** and log in with your Twitch account.
   Twitch requires two-factor authentication for this page. If it asks, turn it on under *Settings → Security and Privacy*.
2. Click **Register Your Application** and fill in:
   - **Name:** `TrendPilot-` plus your channel name (names must be unique across Twitch)
   - **OAuth Redirect URLs:** `http://localhost`
   - **Category:** Analytics Tool
   - **Client Type:** Confidential
3. Tick the "I'm not a robot" box and click **Create**.
4. Click **Manage** next to your new app.
5. Copy the **Client ID** and paste it into TrendPilot.
6. Click **New Secret**, copy the secret, and paste it into TrendPilot. Twitch only shows the secret once.

✅ You should see `Twitch app works`.

> This only lets TrendPilot read **public** information (which games are live and how many people are watching). It can't touch your channel.

---

## Step 4: AI writer

Used only by `/suggest`. Everything else works without it. Pick one:

| Option | Cost | How to get the key |
|---|---|---|
| **1) Groq** (recommended) | Free tier, no card | <https://console.groq.com/keys> → **Create API Key** |
| 2) OpenRouter | Free models available | <https://openrouter.ai/keys> → **Create Key** |
| 3) OpenAI | Paid, a few cents a month | <https://platform.openai.com/api-keys> → **Create new secret key** |
| 4) Ollama | Free, runs on your computer | Install from <https://ollama.com>, then run `ollama pull llama3.1`. No key needed. Uses your graphics card, so avoid it on the PC you stream from. |

Type the number, paste the key, and press Enter to accept the suggested model.

✅ You should see `AI writer works`. If it says the model wasn't found, the provider has renamed it. Open the provider's model list, copy any current chat model name, and paste it when asked.

---

## Step 5: Region and time zone

- **Country code:** where your audience mostly is. `US`, `GB`, `CA`, `AU`, `BR`, `DE` and so on.
- **Language:** type `en` to count only English-language Twitch streams (`es` Spanish, `pt` Portuguese, `de` German…). Press Enter to count everyone.
- **Time zone:** TrendPilot detects it. Press Enter to accept. `/besttime` shows hours in this zone.

---

## Last step: add the bot to your server

At the end, TrendPilot prints a link that starts with `https://discord.com/oauth2/authorize`.

1. Open the link in your browser.
2. Choose your server and click **Continue** → **Authorize**.

Now double-click TrendPilot again. Its window shows `discord bot online`. In Discord, type `/` and the TrendPilot commands appear.

> **Commands not showing?** New bots can take a few minutes to appear everywhere. For instant setup: in Discord turn on *User Settings → Advanced → Developer Mode*, right-click your server icon → **Copy Server ID**, and add it to `trendpilot.env` as `DISCORD_GUILD_ID=...`. Then restart TrendPilot.

---

## Keeping it running

- TrendPilot runs as long as its window is open. You can minimise the window.
- `/besttime` learns from every hour TrendPilot runs. A week of running gives full weekly patterns.
- **Updating:** download the new version and replace the old `trendpilot` file. Keep your `trendpilot.env`, because it has your keys.

---

## Troubleshooting

| You see | What to do |
|---|---|
| Window flashes and closes | Open the folder, click the address bar, type `cmd` and press Enter, then type `trendpilot` and press Enter. The window stays open so you can read the message. |
| `Discord rejected that token` | Go back to the Bot tab, click **Reset Token** again, and copy the new one. |
| `youtube: HTTP 403 ... quota` | The day's free allowance is used up. It resets at midnight Pacific time. |
| `youtube: HTTP 403 ... has not been used in project` | Step 2.3 was missed. Enable **YouTube Data API v3** for the project. |
| `twitch auth: HTTP 403` / `invalid client secret` | Make a **New Secret** in the Twitch console and paste it again. |
| `First scan of Twitch is still running` | Wait a minute after starting. The first scan takes about 10–20 seconds. |
| `/besttime` says no history yet | Normal on the first day. It needs a few hours of running. |
| Mac: "cannot be opened because the developer cannot be verified" | **System Settings → Privacy & Security** → scroll down → **Open Anyway**. |
| Windows: "Windows protected your PC" | Click **More info → Run anyway**. |

Still stuck? [Open an issue](https://github.com/Mathiew1618/trendpilot/issues) and paste the message you see. **Remove any keys from it first.**
