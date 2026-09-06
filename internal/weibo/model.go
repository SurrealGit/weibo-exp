package weibo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var topicIDPattern = regexp.MustCompile(`(?i)100808[0-9a-z]{32}`)

type LoginStatus struct {
	Login bool   `json:"login"`
	ST    string `json:"st"`
	UID   string `json:"uid"`
}

type Topic struct {
	ID         string
	Name       string
	ButtonName string
	CheckinURL string
	Signed     bool
	CanCheckin bool
}

type Post struct {
	MID      string
	UID      string
	UserName string
	IsSelf   bool
}

type card struct {
	CardGroup []card `json:"card_group"`
	TitleSub  any    `json:"title_sub"`
	Scheme    any    `json:"scheme"`
	ItemID    any    `json:"itemid"`
	Buttons   []struct {
		Name   any `json:"name"`
		Scheme any `json:"scheme"`
	} `json:"buttons"`
	Mblog *struct {
		MID  any `json:"mid"`
		ID   any `json:"id"`
		User struct {
			ID         any    `json:"id"`
			ScreenName string `json:"screen_name"`
		} `json:"user"`
	} `json:"mblog"`
}

type cardsData struct {
	Cards        []card `json:"cards"`
	CardListInfo struct {
		SinceID any `json:"since_id"`
	} `json:"cardlistInfo"`
}

func ParseTopics(data json.RawMessage) ([]Topic, string, error) {
	var payload cardsData
	if err := decodeNumbers(data, &payload); err != nil {
		return nil, "", fmt.Errorf("解析关注超话: %w", err)
	}
	seen := make(map[string]bool)
	var result []Topic
	var visit func(card)
	visit = func(value card) {
		for _, nested := range value.CardGroup {
			visit(nested)
		}
		title := stringValue(value.TitleSub)
		if title == "" {
			return
		}
		itemID := stringValue(value.ItemID)
		id := topicID(stringValue(value.Scheme))
		if id == "" {
			id = topicID(itemID)
		}
		if id == "" || seen[id] {
			return
		}
		var buttonName, buttonURL string
		for _, button := range value.Buttons {
			name := stringValue(button.Name)
			if buttonName == "" {
				buttonName = name
				buttonURL = stringValue(button.Scheme)
			}
			if strings.Contains(name, "签到") || strings.Contains(name, "已签") {
				buttonName = name
				buttonURL = stringValue(button.Scheme)
				break
			}
		}
		hasTaskButton := strings.Contains(buttonName, "签到") || strings.Contains(buttonName, "已签")
		if !strings.HasPrefix(itemID, "follow_super_follow_") && !hasTaskButton {
			return
		}
		seen[id] = true
		result = append(result, Topic{
			ID: id, Name: title, ButtonName: buttonName, CheckinURL: buttonURL,
			Signed:     strings.Contains(buttonName, "已签"),
			CanCheckin: strings.Contains(buttonName, "签到") && !strings.Contains(buttonName, "已签"),
		})
	}
	for _, value := range payload.Cards {
		visit(value)
	}
	return result, stringValue(payload.CardListInfo.SinceID), nil
}

func ParsePosts(data json.RawMessage, selfUID string) ([]Post, string, error) {
	var payload cardsData
	if err := decodeNumbers(data, &payload); err != nil {
		return nil, "", fmt.Errorf("解析超话帖子: %w", err)
	}
	seen := make(map[string]bool)
	var result []Post
	var visit func(card)
	visit = func(value card) {
		for _, nested := range value.CardGroup {
			visit(nested)
		}
		if value.Mblog == nil {
			return
		}
		mid := stringValue(value.Mblog.MID)
		if mid == "" {
			mid = stringValue(value.Mblog.ID)
		}
		if mid == "" || seen[mid] {
			return
		}
		seen[mid] = true
		uid := stringValue(value.Mblog.User.ID)
		result = append(result, Post{
			MID: mid, UID: uid, UserName: value.Mblog.User.ScreenName,
			IsSelf: selfUID != "" && uid == selfUID,
		})
	}
	for _, value := range payload.Cards {
		visit(value)
	}
	return result, stringValue(payload.CardListInfo.SinceID), nil
}

func CreatedID(data json.RawMessage) string {
	var value any
	if decodeNumbers(data, &value) != nil {
		return ""
	}
	root, _ := value.(map[string]any)
	if root == nil {
		return ""
	}
	dataMap, _ := root["data"].(map[string]any)
	candidates := []any{dataMap["idstr"], dataMap["id"], dataMap["mid"]}
	if comment, ok := dataMap["comment"].(map[string]any); ok {
		candidates = append(candidates, comment["idstr"], comment["id"])
	}
	if comment, ok := root["comment"].(map[string]any); ok {
		candidates = append(candidates, comment["idstr"], comment["id"])
	}
	candidates = append(candidates, root["idstr"], root["id"], root["mid"])
	for _, candidate := range candidates {
		if result := stringValue(candidate); result != "" {
			return result
		}
	}
	return ""
}

func topicID(value string) string {
	return topicIDPattern.FindString(value)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		if typed == "0" {
			return ""
		}
		return typed
	case float64:
		if typed == 0 {
			return ""
		}
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		if typed.String() == "0" {
			return ""
		}
		return typed.String()
	case bool:
		if !typed {
			return ""
		}
		return "true"
	default:
		return fmt.Sprint(typed)
	}
}

func decodeNumbers(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}
