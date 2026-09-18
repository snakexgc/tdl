package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type updatePortStub struct{ checks, downloads int }

func (u *updatePortStub) Check(context.Context) (types.UpdateInfo, error) {
	u.checks++
	return types.UpdateInfo{LatestVersion: "component-release"}, nil
}

func (u *updatePortStub) Download(context.Context) (types.UpdatePlan, types.UpdateInfo, error) {
	u.downloads++
	return types.UpdatePlan{}, types.UpdateInfo{LatestVersion: "component-release"}, nil
}

func TestUpdateUsesInjectedPort(t *testing.T) {
	service := &updatePortStub{}
	s := NewServer(Options{Updater: service})
	request := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	response := httptest.NewRecorder()
	s.handleUpdateCheck(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "component-release")
	_, _, err := s.downloadUpdate(request)
	require.NoError(t, err)
	require.Equal(t, 1, service.checks)
	require.Equal(t, 1, service.downloads)
}
