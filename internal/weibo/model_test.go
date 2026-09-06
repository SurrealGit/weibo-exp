package weibo

import (
	"encoding/json"
	"testing"
)

const testTopicID = "10080829fcaf31d04023b2329c1f7dce4941df"

func TestParseTopicsAndPosts(t *testing.T) {
	topicsJSON := json.RawMessage(`{
      "cards":[
        {"card_group":[
          {"title_sub":"测试","scheme":"https://m.weibo.cn/p/index?containerid=` + testTopicID + `","itemid":"follow_super_follow_x","buttons":[{"name":"签到","scheme":"/api/container/btn?st=x"}]},
          {"title_sub":"无签到链接","scheme":"https://m.weibo.cn/p/index?containerid=100808bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","itemid":"follow_super_follow_y","buttons":[{"name":"去看看","scheme":false}]},
          {"title_sub":"推荐","scheme":"https://m.weibo.cn/p/index?containerid=100808aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","itemid":"recommend"}
        ]}
      ],
      "cardlistInfo":{"since_id":"next"}
    }`)
	topics, next, err := ParseTopics(topicsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 || topics[0].ID != testTopicID || !topics[0].CanCheckin || topics[1].CheckinURL != "" || next != "next" {
		t.Fatalf("解析结果不符合预期: %#v next=%q", topics, next)
	}

	postsJSON := json.RawMessage(`{
      "cards":[
        {"mblog":{"mid":"1001","user":{"id":"42","screen_name":"自己"}}},
        {"card_group":[{"mblog":{"id":1002,"user":{"id":"43","screen_name":"他人"}}}]}
      ],
      "cardlistInfo":{"since_id":0}
    }`)
	posts, next, err := ParsePosts(postsJSON, "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || !posts[0].IsSelf || posts[1].MID != "1002" || next != "" {
		t.Fatalf("帖子解析结果不符合预期: %#v next=%q", posts, next)
	}
}

func TestCreatedID(t *testing.T) {
	for _, input := range []string{
		`{"data":{"idstr":"123"}}`,
		`{"id":5339546700223599}`,
		`{"data":{"comment":{"id":789}}}`,
	} {
		if CreatedID(json.RawMessage(input)) == "" {
			t.Fatalf("未能解析创建 ID: %s", input)
		}
	}
	if got := CreatedID(json.RawMessage(`{"id":5339546700223599}`)); got != "5339546700223599" {
		t.Fatalf("大整数 ID 发生精度丢失: %q", got)
	}
}

func TestParseTopicsFindsCheckinBeyondFirstButton(t *testing.T) {
	data := json.RawMessage(`{"cards":[{"title_sub":"示例","scheme":"https://m.weibo.cn/p/index?containerid=` + testTopicID + `","itemid":"follow_super_follow_x","buttons":[{"name":"去看看","scheme":"/view"},{"name":"已签到","scheme":"/checkin"}]}]}`)
	topics, _, err := ParseTopics(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 || !topics[0].Signed || topics[0].ButtonName != "已签到" {
		t.Fatalf("未遍历全部按钮：%#v", topics)
	}
}
