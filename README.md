<p align="center">
  <img src="docs/img/banner.jpg" alt="TrendPilot: your content navigation bot" width="100%">
</p>

<h1 align="center">TrendPilot</h1>
<p align="center"><b>Know what's trending, when to go live, and how to word your post, all from Discord.</b><br>
Free and open source. Runs quietly in the background while you stream.</p>

<p align="center">
  <a href="https://github.com/Mathiew1618/trendpilot/releases/latest"><b>⬇️ Download</b></a> ·
  <a href="docs/SETUP.md"><b>Setup guide</b></a> ·
  <a href="#-will-it-slow-down-my-stream">Will it slow my stream?</a> ·
  <a href="#-questions">Questions</a>
</p>

---

## What it does

TrendPilot is a bot that lives in your Discord server. Ask it a question with a slash command and it answers from **live Twitch and YouTube data**.

| Ask it | You get |
|---|---|
| `/games` | Games trending on Twitch **right now**, how many people are watching each, and which ones a smaller channel can actually get noticed in |
| `/genres` | Which genres are pulling viewers today (shooters, RPGs, horror…) |
| `/besttime` | The best hours **for you** to go live or post, in your own time zone |
| `/viewers` | Is Twitch busier or quieter than an hour ago? What's rising and what's fading? |
| `/trends` | Hashtags and topics trending on YouTube that are still early (rising, not yet crowded) |
| `/suggest` | Paste your title or caption and get 3 better versions plus hashtags that fit current trends |
| `/watch` | Paste a YouTube link. TrendPilot messages you when new comments come in (reply while it counts) or when the video starts taking off |

<details>
<summary><b>Example: <code>/games</code></b></summary>

```
Trending on Twitch now (412k viewers in the top 30 categories)
1. Just Chatting → 98k viewers · 200+ channels · Non-gaming
2. Grand Theft Auto V ↑ 41k viewers · 200+ channels · Shooter/Racing
3. Hollow Knight: Silksong ↑ 22k viewers · 200+ channels · Platform/Adventure · ▶️ 3 on YouTube trending
…

Best openings for a growing channel (big audience, low bar to reach the first screens)
1. Hollow Knight: Silksong: 22k viewers, ~35 viewers gets you top 25, rising 18%/h
2. Hades II: 9.1k viewers, ~14 viewers gets you top 25
…
```
*(Illustration of the format. Your numbers come from live data.)*
</details>

### How "best openings" works

A huge game isn't automatically a good choice. If the 25th-biggest stream has 2,000 viewers, nobody scrolling the directory ever reaches you.
TrendPilot looks for games with **lots of viewers where a small stream can still appear near the top of the directory**, and gives extra credit to games that are rising right now.

---

## ⬇️ Get started (about 15 minutes, once)

1. **Download** the file for your computer from the [Releases page](https://github.com/Mathiew1618/trendpilot/releases/latest):

   | Your computer | Download |
   |---|---|
   | Windows | `trendpilot_…_windows_amd64.zip` |
   | Mac with Apple chip (M1, M2, M3, M4) | `trendpilot_…_darwin_arm64.tar.gz` |
   | Older Intel Mac | `trendpilot_…_darwin_amd64.tar.gz` |
   | Linux | `trendpilot_…_linux_amd64.tar.gz` |

2. **Unzip it** and **double-click `trendpilot`**.
3. **Follow the setup.** It walks you through getting four free keys (Discord, YouTube, Twitch and an AI writer), checks each one works, and gives you the link to add the bot to your server.

After that, double-click TrendPilot whenever you want it running. That's it.

> **Stuck on a step?** The [setup guide](docs/SETUP.md) goes through each one in detail.
> **Windows says "Windows protected your PC"?** Click **More info → Run anyway**. That appears for any new app that isn't from a big publisher.
> **Mac says it can't be opened?** Open **System Settings → Privacy & Security**, scroll down, and click **Open Anyway** next to TrendPilot. You only need to do this once.

---

## 🎮 Will it slow down my stream?

**No.** TrendPilot was built for exactly this worry.

### What it uses

<!-- METRICS:START -->
_Measured numbers are being collected and will appear here._
<!-- METRICS:END -->

### Why it can't get in your stream's way

- **It runs at low priority.** Your computer always serves OBS, your game and your encoder first. TrendPilot only gets the leftovers.
- **It's limited to one CPU core**, even on a 16-core machine.
- **It has a memory ceiling of 150 MB**, less than a single browser tab.
- **It mostly sleeps.** It wakes up every few minutes, makes a handful of small requests, and goes back to sleep.
- **It doesn't touch your stream.** It never reads your video, your audio or OBS, and it doesn't upload anything big.
- **You can check it yourself.** It writes its own CPU, memory and data use to its window every hour.

---

## 🔒 Privacy and fair play

- **Your keys stay on your computer** in a file called `trendpilot.env`. TrendPilot has no server of its own and collects nothing.
- **The only thing saved to disk** is a small file of hourly viewer averages per game, which is how `/besttime` learns. It contains no personal data.
- **TrendPilot never posts, likes or comments for you.** Fake engagement breaks every platform's rules and can get your channel restricted. TrendPilot tells you when to act, and you do it yourself.
- **It only suggests hashtags that fit your content.** Trending tags that don't fit are listed as skipped, because off-topic tags get posts marked as spam.

---

## ❓ Questions

<details><summary><b>Does it cost anything?</b></summary>

TrendPilot itself is free. All four keys have free tiers that are plenty for one creator: YouTube gives 10,000 requests a day and TrendPilot uses about 600, and Twitch and Discord are free. For the AI writer, Groq has a free tier, or you can run Ollama on your own computer for free.
</details>

<details><summary><b>Do I have to leave my computer on?</b></summary>

TrendPilot only works while it's running. Most people start it when they sit down to stream or edit. `/besttime` gets smarter the more hours it has seen, so leaving it running for a week gives the best results. A spare laptop or a Raspberry Pi works great for that.
</details>

<details><summary><b>Why does <code>/besttime</code> say "still learning"?</b></summary>

Twitch doesn't publish viewer history, so TrendPilot builds its own by checking every 10 minutes. You get useful answers after a few hours and full weekly patterns after about 7 days. That history is kept between restarts.
</details>

<details><summary><b>Does it work for TikTok or Instagram?</b></summary>

`/suggest` writes captions and hashtags for TikTok, Instagram, YouTube and Twitch. The live trend data comes from YouTube and Twitch, because TikTok and Instagram don't offer public trend data to independent apps.
</details>

<details><summary><b>Can my mods use it?</b></summary>

Yes. Anyone in your Discord server can use the commands. Alerts from `/watch` go to the channel where it was run.
</details>

<details><summary><b>How do I stop it?</b></summary>

Close its window, or press Ctrl+C in it.
</details>

---

## For developers

TrendPilot is written in Go: a single file, no installer, no background services. See [docs/DEVELOPING.md](docs/DEVELOPING.md) to build it, run the tests, or contribute.

Released under the [MIT License](LICENSE).
