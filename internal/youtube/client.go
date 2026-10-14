// Package youtube is a minimal client for the read-only parts of the YouTube
// Data API v3 that TrendPilot needs. An API key is enough; no OAuth.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBase = "https://www.googleapis.com/youtube/v3"

type Video struct {
	ID           string
	Title        string
	Description  string
	ChannelID    string
	ChannelTitle string
	CategoryID   string
	Tags         []string
	PublishedAt  time.Time
	Views        int64
	Likes        int64
	Comments     int64
}

type Client struct {
	Key  string
	Base string
	HTTP *http.Client
}

func New(key string) *Client {
	return &Client{Key: key, Base: defaultBase, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// MostPopular returns the region's trending chart (videos.list chart=mostPopular,
// 1 quota unit per call). category may be empty for all categories.
func (c *Client) MostPopular(ctx context.Context, region, category string, max int) ([]Video, error) {
	q := url.Values{
		"part":       {"snippet,statistics"},
		"chart":      {"mostPopular"},
		"regionCode": {region},
		"maxResults": {strconv.Itoa(min(max, 50))},
	}
	if category != "" {
		q.Set("videoCategoryId", category)
	}
	return c.list(ctx, q)
}

// Stats fetches current statistics for the given video IDs, 50 per call.
func (c *Client) Stats(ctx context.Context, ids []string) ([]Video, error) {
	var out []Video
	for start := 0; start < len(ids); start += 50 {
		end := min(start+50, len(ids))
		q := url.Values{"part": {"snippet,statistics"}, "id": {strings.Join(ids[start:end], ",")}}
		vs, err := c.list(ctx, q)
		if err != nil {
			return out, err
		}
		out = append(out, vs...)
	}
	return out, nil
}

type listResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			PublishedAt  time.Time `json:"publishedAt"`
			ChannelID    string    `json:"channelId"`
			ChannelTitle string    `json:"channelTitle"`
			Title        string    `json:"title"`
			Description  string    `json:"description"`
			CategoryID   string    `json:"categoryId"`
			Tags         []string  `json:"tags"`
		} `json:"snippet"`
		Statistics struct {
			ViewCount    string `json:"viewCount"`
			LikeCount    string `json:"likeCount"`
			CommentCount string `json:"commentCount"`
		} `json:"statistics"`
	} `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) list(ctx context.Context, q url.Values) ([]Video, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("youtube: YOUTUBE_API_KEY is not set")
	}
	q.Set("key", c.Key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/videos?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var lr listResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("youtube: decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if lr.Error != nil {
		return nil, fmt.Errorf("youtube: HTTP %d: %s", lr.Error.Code, lr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube: HTTP %d", resp.StatusCode)
	}
	out := make([]Video, 0, len(lr.Items))
	for _, it := range lr.Items {
		s := it.Snippet
		out = append(out, Video{
			ID: it.ID, Title: s.Title, Description: s.Description,
			ChannelID: s.ChannelID, ChannelTitle: s.ChannelTitle, CategoryID: s.CategoryID,
			Tags: s.Tags, PublishedAt: s.PublishedAt,
			Views:    atoi(it.Statistics.ViewCount),
			Likes:    atoi(it.Statistics.LikeCount),
			Comments: atoi(it.Statistics.CommentCount),
		})
	}
	return out, nil
}

// Counts arrive as strings and are omitted when the owner hides them.
func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
