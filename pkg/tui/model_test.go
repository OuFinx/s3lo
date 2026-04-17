package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel() RootModel {
	return New(context.Background(), "local://./teststore/", nil, "teststore", "")
}

func TestRootModel_ImagesFetched_PopulatesPane(t *testing.T) {
	m := newTestModel()
	entries := []ImageListEntry{
		{Name: "alpine", TagCount: 2, TotalBytes: 50 << 20},
		{Name: "myapp", TagCount: 5, TotalBytes: 900 << 20},
	}
	next, _ := m.Update(imagesFetchedMsg{entries: entries})
	rm := next.(RootModel)

	if rm.leftPane.SelectedImageName() != "alpine" {
		t.Errorf("expected cursor at first entry 'alpine', got %q", rm.leftPane.SelectedImageName())
	}
	if rm.err != nil {
		t.Errorf("unexpected fatal error: %v", rm.err)
	}
}

func TestRootModel_ImagesFetched_ErrorSetsFatal(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(imagesFetchedMsg{err: errors.New("bucket not found")})
	rm := next.(RootModel)

	if rm.err == nil {
		t.Fatal("expected fatal error to be set")
	}
}

func TestRootModel_FatalError_OnlyQuitAllowed(t *testing.T) {
	m := newTestModel()
	m.err = errors.New("fatal")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	rm := next.(RootModel)
	if rm.err == nil {
		t.Error("fatal error should not be cleared by 'd' key")
	}
	if cmd != nil {
		t.Error("expected no cmd from non-quit key when fatal error set")
	}

	_, qCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if qCmd == nil {
		t.Error("expected quit cmd from 'q' when fatal error set")
	}
}

func TestRootModel_EnterKey_SwapsToTagListPane(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(imagesFetchedMsg{entries: []ImageListEntry{{Name: "myapp", TagCount: 3}}})
	m = next.(RootModel)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm := next.(RootModel)

	if rm.leftPane.SelectedImageName() != "myapp" {
		t.Errorf("expected leftPane in tag mode for 'myapp', got SelectedImageName=%q", rm.leftPane.SelectedImageName())
	}
	if _, ok := rm.leftPane.(TagListPane); !ok {
		t.Errorf("expected leftPane to be TagListPane after Enter, got %T", rm.leftPane)
	}
}

func TestRootModel_TagsFetched_PopulatesPane(t *testing.T) {
	m := newTestModel()
	m.leftPane = newTagListPane("myapp")

	tags := []TagEntry{
		{Name: "v2.0.0", LastModified: time.Now()},
		{Name: "v1.0.0", LastModified: time.Now().Add(-24 * time.Hour)},
	}
	next, _ := m.Update(tagsFetchedMsg{imageName: "myapp", tags: tags})
	rm := next.(RootModel)

	if rm.leftPane.SelectedTagName() != "v2.0.0" {
		t.Errorf("expected cursor at newest tag 'v2.0.0', got %q", rm.leftPane.SelectedTagName())
	}
}

func TestRootModel_EscKey_ReturnsToImageList(t *testing.T) {
	m := newTestModel()
	m.leftPane = newTagListPane("myapp")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	rm := next.(RootModel)

	if _, ok := rm.leftPane.(ImageListPane); !ok {
		t.Errorf("expected ImageListPane after Esc, got %T", rm.leftPane)
	}
}

func TestRootModel_DKey_OpensConfirmDialog(t *testing.T) {
	m := newTestModel()
	m.leftPane = newTagListPane("myapp")
	next, _ := m.Update(tagsFetchedMsg{
		imageName: "myapp",
		tags:      []TagEntry{{Name: "v1.0.0", LastModified: time.Now()}},
	})
	m = next.(RootModel)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	rm := next.(RootModel)

	if rm.overlay == nil {
		t.Fatal("expected overlay to be set after 'd' key")
	}
	if _, ok := rm.overlay.(ConfirmDialog); !ok {
		t.Errorf("expected ConfirmDialog overlay, got %T", rm.overlay)
	}
}

func TestRootModel_DeleteResult_Error_SetsStatus(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(deleteResultMsg{err: errors.New("access denied")})
	rm := next.(RootModel)

	if !rm.statusErr {
		t.Error("expected statusErr to be true after delete error")
	}
	if rm.status == "" {
		t.Error("expected status message after delete error")
	}
}

func TestRootModel_DeleteResult_Success_ClearsOverlay(t *testing.T) {
	m := newTestModel()
	m.overlay = newConfirmDialog("Delete", "Are you sure?", "delete", "s3://b/img:tag")

	next, _ := m.Update(deleteResultMsg{err: nil})
	rm := next.(RootModel)

	if rm.overlay != nil {
		t.Error("expected overlay to be nil after successful delete")
	}
}

func TestRootModel_StatusClear_ClearsStatus(t *testing.T) {
	m := newTestModel()
	m.status = "some error"
	m.statusErr = true

	next, _ := m.Update(statusClearMsg{})
	rm := next.(RootModel)

	if rm.status != "" {
		t.Errorf("expected empty status after statusClearMsg, got %q", rm.status)
	}
}

func TestRootModel_TagsFetched_SetsStatsPanelToFirstTag(t *testing.T) {
	m := newTestModel()
	m.leftPane = newTagListPane("myapp")
	m.right = m.right.SetTagMode("myapp", "")

	tags := []TagEntry{
		{Name: "latest", LastModified: time.Now()},
		{Name: "v1.0.0", LastModified: time.Now().Add(-24 * time.Hour)},
	}
	next, _ := m.Update(tagsFetchedMsg{imageName: "myapp", tags: tags})
	rm := next.(RootModel)

	// Stats panel must be set to the first tag so tagStatsFetchedMsg can match.
	if rm.right.tagName != "latest" {
		t.Errorf("expected right panel tagName='latest', got %q", rm.right.tagName)
	}
}

func TestRootModel_QKey_Quits(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected quit cmd from 'q' key")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}
