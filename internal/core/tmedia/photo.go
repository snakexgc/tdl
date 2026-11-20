package tmedia

import (
	"slices"
	"strconv"

	"github.com/gotd/td/tg"
)

func GetPhotoInfo(photo *tg.MessageMediaPhoto) (*Media, bool) {
	return PhotoVariant(photo, "")
}

// PhotoVariant selects the Telegram thumbnail type recorded by a file ID.
// An empty type requests the largest size, as for a regular chat download.
func PhotoVariant(photo *tg.MessageMediaPhoto, kind string) (*Media, bool) {
	p, ok := photo.Photo.(*tg.Photo)
	if !ok {
		return nil, false
	}

	sizes := p.Sizes
	if kind != "" {
		i := slices.IndexFunc(sizes, func(s tg.PhotoSizeClass) bool { return s.GetType() == kind })
		if i < 0 {
			return nil, false
		}
		sizes = sizes[i : i+1]
	}
	tp, size, ok := GetPhotoSize(sizes)
	if !ok {
		return nil, false
	}
	return &Media{
		InputFileLoc: &tg.InputPhotoFileLocation{
			ID:            p.ID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     tp,
		},
		// Telegram photo is compressed, and extension is always jpg.
		Name: strconv.FormatInt(p.ID, 10) + ".jpg", // unique name
		Size: int64(size),
		DC:   p.DCID,
		Date: int64(p.Date),
	}, true
}

func GetPhotoSize(sizes []tg.PhotoSizeClass) (string, int, bool) {
	if len(sizes) == 0 {
		return "", 0, false
	}
	size := sizes[len(sizes)-1]
	switch s := size.(type) {
	case *tg.PhotoSize:
		return s.Type, s.Size, true
	case *tg.PhotoSizeProgressive:
		if len(s.Sizes) == 0 {
			return "", 0, false
		}
		return s.Type, s.Sizes[len(s.Sizes)-1], true
	}

	return "", 0, false
}
