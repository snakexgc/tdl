package namingrules

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/snakexgc/tdl/application/naming.rules/tplfunc"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/targetpath"
)

const (
	ID             = "naming.rules"
	filenameField  = "filename"
	directoryField = "directory"
	maxBytesField  = "max_bytes"
)

type settings struct {
	pattern   string
	directory string
	maxBytes  int
	tpl       *template.Template
}

type Rules struct{ settings atomic.Pointer[settings] }

func Register(registry *rte.Registry) error {
	zero, maximum := int64(0), int64(255)
	return registry.Register(manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: "/config?tab=download"},
		ID:      ID, Title: "目录与文件命名",
		Provides: []manifest.Port{manifest.PortOf[ports.NamingRules](ports.NamingRulesName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: filenameField, Title: "文件名模板", Help: "支持 G 名称、P 来源 ID、I 消息文字、F 原始文件名、S/R 消息 ID、A 相册 ID、Y/M/D 日期，例如 G-I-F。", Type: manifest.String, Default: "P_S_F"},
			{Name: directoryField, Title: "目录模板", Help: "使用与文件名相同的变量；I 会保留中英文及数字并自动截断。", Type: manifest.String, Default: "G\\Y&M"},
			{Name: maxBytesField, Title: "文件名字节上限", Type: manifest.Int, Default: 255, Min: &zero, Max: &maximum},
		},
	}, "download", "文件命名"), func() rte.Component { return &Rules{} })
}

func (r *Rules) Init(ctx context.Context, k rte.Kernel) error {
	if err := r.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.NamingRulesName, r)
}
func (*Rules) Start(context.Context) error { return nil }
func (*Rules) Stop(context.Context) error  { return nil }

func (r *Rules) PrepareConfig(_ context.Context, view config.View) (func(), error) {
	var next settings
	for _, field := range []struct {
		name   string
		target any
	}{{filenameField, &next.pattern}, {directoryField, &next.directory}, {maxBytesField, &next.maxBytes}} {
		if err := view.Get(field.name, field.target); err != nil {
			return nil, err
		}
	}
	next.pattern = fileNameConfigTemplate(next.pattern)
	tpl, err := template.New(ID).Funcs(tplfunc.FuncMap(tplfunc.All...)).Parse(next.pattern)
	if err != nil {
		return nil, fmt.Errorf("filename template: %w", err)
	}
	next.tpl = tpl
	if next.maxBytes == 0 {
		next.maxBytes = 255
	}
	return func() { r.settings.Store(&next) }, nil
}

func (r *Rules) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := r.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (r *Rules) Render(ctx context.Context, in ports.NamingInput) (ports.NamingResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.NamingResult{}, err
	}
	s := r.settings.Load()
	if s == nil {
		return ports.NamingResult{}, fmt.Errorf("naming component is not initialized")
	}
	data := in.Data
	if data.DownloadedAt.IsZero() {
		data.DownloadedAt = time.Now()
	}
	name := in.RenderedName
	if name == "" {
		var err error
		name, err = s.renderFileName(data)
		if err != nil {
			return ports.NamingResult{}, err
		}
	}
	id := data.DirectoryID
	if id == "" {
		id = strconv.FormatInt(data.DialogID, 10)
	}
	directoryData := downloadDirData{
		ID: id, Name: targetpath.SafePathSegment(data.PeerName), MessageTitle: data.MessageTitle,
		MessageID: strconv.Itoa(data.MessageID), TriggerMessageID: strconv.Itoa(data.TriggerMessageID),
		FileName: data.FileName, AlbumID: data.AlbumID, Time: data.DownloadedAt,
	}
	base := targetpath.JoinTargetPath(in.BaseDir, renderDownloadDir(s.directory, directoryData)...)
	dir, out, full := targetpath.ResolveTargetPath(base, name)
	return ports.NamingResult{FileName: name, Dir: dir, Out: out, FullPath: full, MaxBytes: s.maxBytes}, nil
}
