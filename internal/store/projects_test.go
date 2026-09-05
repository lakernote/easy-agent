package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProjectsPersistDirectoriesAndSessionAssignment(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	first := filepath.Join(t.TempDir(), "api")
	second := filepath.Join(t.TempDir(), "web")
	project, err := database.CreateProject("project-1", "EasyAgent", []string{first, second}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !project.Default || len(project.Directories) != 2 || project.Directories[1] != second {
		t.Fatalf("项目源文件夹没有按顺序保存: %+v", project)
	}
	session, err := database.CreateSessionWithProject("session-1", "检查项目", RuntimeEasyAgent, "", "qwen", project.ID, first, now)
	if err != nil {
		t.Fatal(err)
	}
	if session.ProjectID != project.ID || session.Workspace != first {
		t.Fatalf("会话项目归属错误: %+v", session)
	}
	if err := database.UpdateSessionMetadata(session.ID, "新的名称", project.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := database.LoadSession(session.ID)
	if err != nil || updated.Title != "新的名称" || updated.ProjectID != project.ID {
		t.Fatalf("会话元数据没有更新: %+v err=%v", updated, err)
	}
}

func TestDeleteDefaultProjectPromotesReplacement(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if _, err := database.CreateProject("default", "Default", []string{t.TempDir()}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateProject("replacement", "Replacement", []string{t.TempDir()}, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProject("default"); err != nil {
		t.Fatal(err)
	}
	replacement, err := database.GetProject("replacement")
	if err != nil || !replacement.Default {
		t.Fatalf("删除默认项目后没有提升替代项目: %+v err=%v", replacement, err)
	}
	if err := database.DeleteProject("replacement"); err == nil || err.Error() != "至少需要保留一个项目" {
		t.Fatalf("最后一个项目应被保护，实际错误: %v", err)
	}
}
