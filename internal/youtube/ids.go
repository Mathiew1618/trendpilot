package youtube

import (
	"net/url"
	"regexp"
	"strings"
)

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// VideoID accepts a bare ID or any common YouTube link (watch, youtu.be,
// shorts, live, embed) and returns the 11-character video ID.
func VideoID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if idRE.MatchString(s) {
		return s, true
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	var id string
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "music.youtube.com":
		if v := u.Query().Get("v"); v != "" {
			id = v
		} else {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "live" || parts[0] == "embed") {
				id = parts[1]
			}
		}
	}
	if idRE.MatchString(id) {
		return id, true
	}
	return "", false
}
