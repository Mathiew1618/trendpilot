**TrendPilot** is a free Discord bot for streamers and creators. It reads live Twitch and YouTube data and tells you what's trending, when to go live, and how to word your posts. It's light enough to run while you stream: about 0.15% of one CPU core, 15 MB of memory, and under 1 kbps of upload ([measured](https://github.com/Mathiew1618/trendpilot#-will-it-slow-down-my-stream)).

## ➕ Easiest: add it to Discord

No download needed: **[add the hosted TrendPilot to your server](https://discord.com/oauth2/authorize?client_id=1560014656339189769&scope=bot+applications.commands&permissions=3072)**, then run `/mychannel`, `/timezone` and `/sendhere`. What's new: [CHANGELOG](https://github.com/Mathiew1618/trendpilot/blob/main/CHANGELOG.md).

## ⬇️ Or run your own copy: which file do I download?

| Your computer | File |
|---|---|
| **Windows** | `trendpilot_*_windows_amd64.zip` |
| **Mac with Apple chip** (M1, M2, M3, M4) | `trendpilot_*_darwin_arm64.tar.gz` |
| **Older Intel Mac** | `trendpilot_*_darwin_amd64.tar.gz` |
| **Linux** | `trendpilot_*_linux_amd64.tar.gz` (Raspberry Pi: `_linux_arm64`) |

Then:
1. Unzip it.
2. Double-click **trendpilot**.
3. Follow the setup, which takes about 15 minutes, once.

**SETUP.md** inside the download walks through every step. To change your keys later, double-click **Change settings**.

> **Windows** may say "Windows protected your PC": click **More info → Run anyway**.
> **Mac** may say it can't be opened: go to **System Settings → Privacy & Security** and click **Open Anyway**.
> Both warnings appear for any new app that isn't from a big publisher.

## What's in it

- `/games`, `/genres`, `/viewers`, `/besttime`: what's trending on Twitch now, which genres are pulling viewers, and the best hours for *you* to go live
- `/trends`, `/suggest`: early YouTube hashtags, and AI rewordings of your title with hashtags that fit
- `/watch`: alerts when a YouTube video gets comments or takes off
- `/raid`: small channels in your game that a raid would really help
- **Automatic:** trend-shift alerts, AI go-live posts, and chat-surge clips sent to Discord for one-click approval
- **Per server:** `/mychannel`, `/connect-twitch`, `/timezone`, `/settings`, `/reset`

TrendPilot never posts, likes or comments for you, and never raids unless you press the button.

`checksums.txt` lets you check your download wasn't altered.
