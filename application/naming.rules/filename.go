package namingrules

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/targetpath"
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

func (s *settings) renderFileName(data ports.NamingData) (string, error) {
	dialogID, peerName, downloadedAt := data.DialogID, data.PeerName, data.DownloadedAt
	messageTitle, triggerMessageID, albumID := data.MessageTitle, data.TriggerMessageID, data.AlbumID

	ext := filepath.Ext(data.FileName)
	stem := strings.TrimSuffix(data.FileName, ext)
	fValue := targetpath.SafePathSegment(stem)
	hasI := strings.Contains(s.pattern, "{{ .I }}")

	appendExt := func(s string) string {
		// collapse consecutive dashes/underscores that may result from empty template vars
		prefix, leaf := targetpath.SplitRenderedNameLeaf(s)
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
		if err := s.tpl.Execute(&toName, &fileTemplate{
			DialogID:         dialogID,
			MessageID:        data.MessageID,
			TriggerMessageID: triggerMessageID,
			MessageDate:      data.MessageDate,
			FileName:         stem,
			FileCaption:      data.Caption,
			MessageTitle:     messageTitle,
			PeerName:         peerName,
			AlbumID:          albumID,
			F:                fValue,
			I:                iValue,
			G:                targetpath.SafePathSegment(peerName),
			P:                fmt.Sprint(dialogID),
			S:                fmt.Sprint(data.MessageID),
			R:                fmt.Sprint(triggerMessageID),
			A:                targetpath.SafePathSegment(albumID),
			FileSize:         formatBinaryBytes(data.FileSize),
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
	maxBytes := s.maxBytes
	if maxBytes <= 0 || maxBytes > filenameHardMaxBytes {
		maxBytes = filenameHardMaxBytes
	}
	if targetpath.RenderedNameLeafByteLen(rendered) <= maxBytes {
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
			if targetpath.RenderedNameLeafByteLen(candidate) <= maxBytes {
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

	return targetpath.LimitRenderedNameLeafBytes(rendered, maxBytes), nil
}
