package schedule

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Check every starting hour, including a single late-night trigger and midnight.
func TestDailyRetriesStopAtMidnight(t *testing.T) {
	for hour := 0; hour < 24; hour++ {
		for _, minute := range []int{0, 7, 59} {
			t.Run(fmt.Sprintf("%02d:%02d", hour, minute), func(t *testing.T) {
				cfg := Config{Executable: filepath.Join(t.TempDir(), "weibo-exp"), DataDir: filepath.Join(t.TempDir(), "data"), Hour: hour, Minute: minute}
				var want []string
				for h := hour; h < 24; h += 4 {
					want = append(want, fmt.Sprintf("%02d:%02d", h, minute))
				}
				plist, err := RenderLaunchAgent(cfg)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, match := range regexp.MustCompile(`<key>Hour</key><integer>([0-9]+)</integer><key>Minute</key><integer>([0-9]+)</integer>`).FindAllStringSubmatch(string(plist), -1) {
					h, _ := strconv.Atoi(match[1])
					m, _ := strconv.Atoi(match[2])
					got = append(got, fmt.Sprintf("%02d:%02d", h, m))
				}
				if !reflect.DeepEqual(got, want) || !launchdFourHourly(string(plist)) {
					t.Fatalf("launchd: got %v, want %v", got, want)
				}
				timer, err := RenderSystemdTimer(cfg)
				if err != nil {
					t.Fatal(err)
				}
				got = nil
				for _, match := range regexp.MustCompile(`OnCalendar=\*-\*-\* ([0-9]{2}:[0-9]{2}):00`).FindAllStringSubmatch(string(timer), -1) {
					got = append(got, match[1])
				}
				if !reflect.DeepEqual(got, want) || !systemdFourHourly(string(timer)) {
					t.Fatalf("systemd: got %v, want %v", got, want)
				}
				data, err := RenderWindowsTaskXML(cfg)
				if err != nil {
					t.Fatal(err)
				}
				var task struct {
					Boundaries []string `xml:"Triggers>CalendarTrigger>StartBoundary"`
				}
				if err := xml.Unmarshal(data, &task); err != nil {
					t.Fatal(err)
				}
				got = nil
				for _, boundary := range task.Boundaries {
					got = append(got, strings.TrimSuffix(strings.TrimPrefix(boundary, "2000-01-01T"), ":00"))
				}
				if !reflect.DeepEqual(got, want) || !windowsFourHourly(data) || strings.Contains(string(data), "<Repetition>") {
					t.Fatalf("Windows: got %v, want %v", got, want)
				}
			})
		}
	}
}

func TestRetryDetectionRejectsOldCrossMidnightSchedule(t *testing.T) {
	var plist, timer strings.Builder
	for _, hour := range []int{10, 14, 18, 22, 2, 6} {
		fmt.Fprintf(&plist, "<key>Hour</key><integer>%d</integer><key>Minute</key><integer>0</integer>", hour)
		fmt.Fprintf(&timer, "OnCalendar=*-*-* %02d:00:00\n", hour)
	}
	if launchdFourHourly(plist.String()) || systemdFourHourly(timer.String()) {
		t.Fatal("old cross-midnight schedule reported as the new daily retry rule")
	}
	old := []byte(`<Task><Triggers><CalendarTrigger><StartBoundary>2000-01-01T10:00:00</StartBoundary><Repetition><Interval>PT4H</Interval><Duration>P1D</Duration></Repetition><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger></Triggers></Task>`)
	if windowsFourHourly(old) {
		t.Fatal("old Windows 24-hour repetition accepted")
	}
}

func TestWindowsRetryDetectionAndInspect(t *testing.T) {
	data, err := RenderWindowsTaskXML(Config{Executable: filepath.Join(t.TempDir(), "exp"), DataDir: filepath.Join(t.TempDir(), "data"), Hour: 10, Minute: 30})
	if err != nil {
		t.Fatal(err)
	}
	fakePlatform(t, "windows", func(string, ...string) ([]byte, error) { return data, nil })
	status, err := inspectWindows()
	if err != nil || status.DailyTime != "10:30" || status.RetryInterval != RetryDisplay {
		t.Fatalf("%+v %v", status, err)
	}
	for _, replacement := range [][2]string{
		{"T14:30:00", "T02:30:00"},
		{"T14:30:00", "T14:31:00"},
		{"<DaysInterval>1</DaysInterval>", "<DaysInterval>2</DaysInterval>"},
		{"<Enabled>true</Enabled>", "<Enabled>false</Enabled>"},
		{"</StartBoundary>", "</StartBoundary><Repetition><Interval>PT4H</Interval></Repetition>"},
	} {
		broken := strings.Replace(string(data), replacement[0], replacement[1], 1)
		if windowsFourHourly([]byte(broken)) {
			t.Fatalf("accepted malformed daily rule: %v", replacement)
		}
	}
}
