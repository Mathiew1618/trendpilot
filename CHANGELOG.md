# Changelog

## v0.2.0 (2026-10-14)

**Add it to Discord in one minute.** A hosted TrendPilot now runs around the clock. [Add it to your server](https://discord.com/oauth2/authorize?client_id=1560014656339189769&scope=bot+applications.commands&permissions=3072) with no download and no keys. Running your own copy still works the same way.

New commands for setting up each server:
- `/mychannel`: your Twitch channel, for go-live posts, chat-surge highlights and raid ideas
- `/connect-twitch` / `/disconnect-twitch`: sign in to Twitch from inside Discord, for automatic clips and 1-click Raid buttons. It's only accepted when you sign in as your `/mychannel` account.
- `/timezone`: `/besttime` now shows *your* hours (`EST`, `PST`, `UK` or names like `Europe/Berlin`)
- `/settings`: how TrendPilot is set up in this server
- `/reset`: delete everything stored for this server

Also:
- Setup commands answer privately, so only the person who ran them sees the reply.
- Go-live posts, highlights and recaps go only to the server that set them up.
- Fair use on the hosted bot: `/suggest` allows one request every 20 s per person and 50 a day per server.
- The README's privacy section lists exactly what's stored and how to delete it.
- New [HOSTING.md](docs/HOSTING.md) for running your own public instance.

## v0.1.0 (2026-10-14)

First release.
- `/games`, `/genres`, `/viewers`, `/besttime`: live Twitch game trends, genres (IGDB), and the best hours to go live
- `/trends`, `/suggest`: early YouTube hashtags, and AI rewordings with hashtags that fit
- `/watch`: comment and breakout alerts for YouTube videos
- `/raid`: small live channels in your game that a raid would really help
- Automatic trend-shift alerts, AI go-live posts, and chat-surge clips sent to Discord for one-click approval
- Setup wizard; downloads for Windows, Mac and Linux; measured at 0.15% of one CPU core and 15 MB of memory
