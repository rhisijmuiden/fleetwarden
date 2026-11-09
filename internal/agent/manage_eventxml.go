package agent

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// eventXML is the part of a rendered Windows event we use.
type eventXML struct {
	System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     string `xml:"EventID"`
		Level       int    `xml:"Level"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		Execution struct {
			ProcessID int `xml:"ProcessID,attr"`
		} `xml:"Execution"`
		Security struct {
			UserID string `xml:"UserID,attr"`
		} `xml:"Security"`
	} `xml:"System"`
	EventData struct {
		Data []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
	UserData struct {
		Inner string `xml:",innerxml"`
	} `xml:"UserData"`
	RenderingInfo struct {
		Message string `xml:"Message"`
	} `xml:"RenderingInfo"`
}

// parseEventXML reads one event as EvtRender returns it. Without a
// formatted message the event data stands in for it.
func parseEventXML(s string) (agentproto.LogEvent, error) {
	var x eventXML
	if err := xml.Unmarshal([]byte(s), &x); err != nil {
		return agentproto.LogEvent{}, err
	}
	e := agentproto.LogEvent{Provider: x.System.Provider.Name, Level: x.System.Level, PID: x.System.Execution.ProcessID,
		User: x.System.Security.UserID, Message: strings.TrimSpace(x.RenderingInfo.Message)}
	if e.Level == 0 {
		e.Level = agentproto.LevelInfo // LogAlways
	}
	// EventID may carry a Qualifiers attribute; the text is the number.
	e.EventID, _ = strconv.Atoi(strings.TrimSpace(x.System.EventID))
	if t, err := time.Parse(time.RFC3339Nano, x.System.TimeCreated.SystemTime); err == nil {
		e.Time = t.UTC()
	}
	if e.Message == "" {
		var parts []string
		for _, d := range x.EventData.Data {
			v := strings.TrimSpace(d.Value)
			if v == "" {
				continue
			}
			if d.Name != "" {
				v = d.Name + ": " + v
			}
			parts = append(parts, v)
		}
		e.Message = strings.Join(parts, "\n")
	}
	return e, nil
}

// validLogName allows channel names such as
// "Microsoft-Windows-Windows Defender/Operational".
func validLogName(n string) bool {
	if n == "" || len(n) > 255 {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" -_/.%", r)) {
			return false
		}
	}
	return true
}

// eventXPath builds the query for a LogQuery.
func eventXPath(q agentproto.LogQuery, now time.Time) string {
	var conds []string
	if q.MaxLevel > 0 && q.MaxLevel < agentproto.LevelVerbose {
		var lv []string
		for l := 1; l <= q.MaxLevel; l++ {
			lv = append(lv, fmt.Sprintf("Level=%d", l))
		}
		if q.MaxLevel >= agentproto.LevelInfo {
			lv = append(lv, "Level=0") // LogAlways
		}
		conds = append(conds, "("+strings.Join(lv, " or ")+")")
	}
	if !q.Since.IsZero() {
		ms := now.Sub(q.Since).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		conds = append(conds, fmt.Sprintf("TimeCreated[timediff(@SystemTime) <= %d]", ms))
	}
	if q.Provider != "" {
		conds = append(conds, fmt.Sprintf("Provider[@Name='%s']", q.Provider))
	}
	if q.EventID > 0 {
		conds = append(conds, fmt.Sprintf("EventID=%d", q.EventID))
	}
	if len(conds) == 0 {
		return "*"
	}
	return "*[System[" + strings.Join(conds, " and ") + "]]"
}
