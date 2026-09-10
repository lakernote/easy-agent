package permissions

import "testing"

func TestNormalizeAndValidate(t *testing.T) {
	value := Policy{Mode: ModeCustom, Approval: ApprovalOnRequest, WritableRoots: []string{"./workspace", "./workspace"}}
	value = value.Normalize()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(value.WritableRoots) != 1 || value.WritableRoots[0][0] != '/' {
		t.Fatalf("可写目录没有规范化: %+v", value.WritableRoots)
	}
	if err := (Policy{Mode: "unknown"}).Validate(); err == nil {
		t.Fatal("未知权限模式应该被拒绝")
	}
	if err := (Policy{Mode: ModeReadOnly, WritableRoots: []string{"/tmp"}}).Validate(); err == nil {
		t.Fatal("只读模式不应允许可写目录")
	}
}
