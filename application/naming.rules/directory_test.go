package namingrules

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testGroupName    = "Group Name"
	testTriggerTitle = "Trigger Title"
	testVideoFile    = "video.mp4"
)

func TestRenderDownloadDirTemplate(t *testing.T) {
	data := downloadDirData{
		ID:               "12345",
		Name:             testGroupName,
		MessageTitle:     testTriggerTitle,
		MessageID:        "7",
		TriggerMessageID: "6",
		FileName:         testVideoFile,
		AlbumID:          "999",
		Time:             time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
	}

	require.Equal(t, []string{"2026", "04", testGroupName}, renderDownloadDir(`Y/M/G`, data))
	require.Equal(t, []string{"202604" + testGroupName}, renderDownloadDir(`Y&M&G`, data))
	require.Equal(t, []string{"202604", testGroupName, "23"}, renderDownloadDir(`Y&M\G\D`, data))
	require.Equal(t, []string{"TriggerTitle", testGroupName}, renderDownloadDir(`I/G`, data))
	require.Equal(t, []string{"12345", "7", "6", "999"}, renderDownloadDir(`P/S/R/A`, data))
	require.Equal(t, []string{"video"}, renderDownloadDir(`F`, data))
}

func TestFileNameConfigTemplateAliases(t *testing.T) {
	require.Equal(t,
		`{{ .G }}-{{ .I }}-{{ .P }}-{{ .S }}-{{ .R }}-{{ .F }}`,
		fileNameConfigTemplate("G-I-P-S-R-F"),
	)
	require.Equal(t,
		`{{ .P }}_{{ .S }}_{{ .F }}`,
		fileNameConfigTemplate("P_S_F"),
	)
}

func TestSafeMessageTitleSegmentKeepsOnlyChineseEnglishDigitsAndCompacts(t *testing.T) {
	require.Equal(t, "标题ABC123", safeMessageTitleSegment(" 标题!@#ABC-123_ "))
	require.Equal(t, "untitled", safeMessageTitleSegment(" !@# -_ "))

	got := safeMessageTitleSegment(strings.Repeat("甲", 60) + "!@#" + strings.Repeat("B", 60))
	require.Equal(t, strings.Repeat("甲", 47)+"..."+strings.Repeat("B", 30), got)
	require.Len(t, []rune(got), safeMessageTitleMaxRunes)
}
