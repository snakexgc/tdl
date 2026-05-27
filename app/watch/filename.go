package watch

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/utils"
)

func fileNameConfigTemplate(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.Contains(pattern, "{{") {
		return pattern
	}

	var b strings.Builder
	for _, r := range pattern {
		if r == '&' {
			continue
		}
		if tpl := fileNameTemplateAlias(r); tpl != "" {
			b.WriteString(tpl)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fileNameTemplateAlias(r rune) string {
	switch r {
	case 'F':
		return `{{ .F }}`
	case 'I':
		return `{{ .I }}`
	case 'G':
		return `{{ .G }}`
	case 'P':
		return `{{ .P }}`
	case 'S':
		return `{{ .S }}`
	case 'R':
		return `{{ .R }}`
	case 'A':
		return `{{ .A }}`
	case 'Y':
		return `{{ formatDate .DownloadDate "2006" }}`
	case 'M':
		return `{{ formatDate .DownloadDate "01" }}`
	case 'D':
		return `{{ formatDate .DownloadDate "02" }}`
	default:
		return ""
	}
}

type fileTemplate struct {
	DialogID         int64
	MessageID        int
	TriggerMessageID int
	MessageDate      int64
	FileName         string
	FileCaption      string
	MessageTitle     string
	PeerName         string
	AlbumID          string
	F                string
	I                string
	G                string
	P                string
	S                string
	R                string
	A                string
	FileSize         string
	DownloadDate     int64
}

func (w *Watcher) renderFileName(dialogID int64, peerName string, downloadedAt time.Time, msg, triggerMsg *tg.Message, media *tmedia.Media) (string, error) {
	if triggerMsg == nil {
		triggerMsg = msg
	}
	if downloadedAt.IsZero() {
		downloadedAt = time.Now()
	}
	messageTitle := ""
	triggerMessageID := 0
	if triggerMsg != nil {
		messageTitle = strings.TrimSpace(triggerMsg.Message)
		triggerMessageID = triggerMsg.ID
	}
	albumID := ""
	if groupedID, ok := msg.GetGroupedID(); ok {
		albumID = fmt.Sprint(groupedID)
	}

	ext := filepath.Ext(media.Name)
	stem := strings.TrimSuffix(media.Name, ext)
	fValue := safePathSegment(stem)
	hasI := strings.Contains(w.opts.Template, "{{ .I }}")

	appendExt := func(s string) string {
		// collapse consecutive dashes/underscores that may result from empty template vars
		prefix, leaf := splitRenderedNameLeaf(s)
		for strings.Contains(leaf, "--") {
			leaf = strings.ReplaceAll(leaf, "--", "-")
		}
		for strings.Contains(leaf, "__") {
			leaf = strings.ReplaceAll(leaf, "__", "_")
		}
		s = prefix + leaf
		if ext != "" && !strings.HasSuffix(s, ext) {
			return s + ext
		}
		return s
	}

	render := func(messageTitleMax int) (string, error) {
		iValue := safeMessageTitleSegmentWithMax(messageTitle, messageTitleMax)
		var toName bytes.Buffer
		if err := w.tpl.Execute(&toName, &fileTemplate{
			DialogID:         dialogID,
			MessageID:        msg.ID,
			TriggerMessageID: triggerMessageID,
			MessageDate:      int64(msg.Date),
			FileName:         stem,
			FileCaption:      msg.Message,
			MessageTitle:     messageTitle,
			PeerName:         peerName,
			AlbumID:          albumID,
			F:                fValue,
			I:                iValue,
			G:                safePathSegment(peerName),
			P:                fmt.Sprint(dialogID),
			S:                fmt.Sprint(msg.ID),
			R:                fmt.Sprint(triggerMessageID),
			A:                safePathSegment(albumID),
			FileSize:         utils.Byte.FormatBinaryBytes(media.Size),
			DownloadDate:     downloadedAt.Unix(),
		}); err != nil {
			return "", errors.Wrap(err, "execute template")
		}
		return appendExt(toName.String()), nil
	}

	rendered, err := render(safeMessageTitleMaxRunes)
	if err != nil {
		return "", err
	}

	const filenameHardMaxBytes = 255
	maxBytes := w.opts.FilenameMaxLength
	if maxBytes <= 0 || maxBytes > filenameHardMaxBytes {
		maxBytes = filenameHardMaxBytes
	}
	if renderedNameLeafByteLen(rendered) <= maxBytes {
		return rendered, nil
	}

	if hasI {
		best := ""
		found := false
		for low, high := 0, safeMessageTitleMaxRunes; low <= high; {
			mid := (low + high) / 2
			candidate, err := render(mid)
			if err != nil {
				return "", err
			}
			if renderedNameLeafByteLen(candidate) <= maxBytes {
				best = candidate
				found = true
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		if found {
			return best, nil
		}
	}

	return limitRenderedNameLeafBytes(rendered, maxBytes), nil
}
