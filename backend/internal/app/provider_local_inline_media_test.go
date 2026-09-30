package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// writeInlineTestImageResource 在服务的本地资源目录里落一张真实 PNG，并返回它的 storage key。
func writeInlineTestImageResource(t *testing.T, svc *Service, name string) string {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(strings.SplitN(testGeminiReferenceImageDataURL, ",", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	key := "users/user-1/image/" + name
	path := filepath.Join(svc.dataDir, "resources", key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.CreateResource(&model.Resource{ID: name, UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: key, MimeType: "image/png", Size: int64(len(png))}); err != nil {
		t.Fatal(err)
	}
	return "resource:" + name
}

// 本地桌面模式下，Agnes 视频协议的参考图必须被内联成 data URL，而不是去要一个
// 上游拉不到的公网地址。这是本次改动的核心验收点。
func TestAgnesVideoInlineMediaInLocalMode(t *testing.T) {
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	for _, protocol := range []string{"agnes-video", "agnes-video-v20"} {
		svc := newResourceTestService(t)
		svc.mode = serviceModeLocal
		svc.localResourceStorage = true
		input := canvasGenerationInput{Mode: "video", Config: providerConfig{InterfaceType: protocol}}
		input.ReferenceImages = []providerMedia{{StorageKey: writeInlineTestImageResource(t, svc, "inline-"+protocol+".png")}}

		// 前置条件：插件声明本身仍然要求公网地址，放行只发生在本地模式的 hydrate 阶段。
		policy := providerMediaHydrationPolicyFor(ctx, input)
		if !policy.requireURL {
			t.Fatalf("%s: 插件声明丢失，requireURL 默认应为 true: %#v", protocol, policy)
		}
		if err := svc.hydrateGenerationMedia("user-1", &input, policy); err != nil {
			t.Fatalf("%s: 本地模式内联失败: %v", protocol, err)
		}
		got := input.ReferenceImages[0]
		if !strings.HasPrefix(got.DataURL, "data:image/png;base64,") {
			t.Fatalf("%s: 期望内联 data URL，实际 URL=%q DataURL=%q", protocol, got.URL, got.DataURL)
		}
		if strings.TrimSpace(got.URL) != "" {
			t.Fatalf("%s: 内联模式不应再设置公网 URL，实际 %q", protocol, got.URL)
		}
	}
}

// 云端/hosted 模式下行为必须完全不变：Agnes 仍然要求公网素材地址，不能内联。
func TestAgnesVideoKeepsPublicURLRequirementInHostedMode(t *testing.T) {
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	for _, protocol := range []string{"agnes-video", "agnes-video-v20"} {
		svc := newResourceTestService(t)
		svc.mode = serviceModeHosted
		input := canvasGenerationInput{Mode: "video", Config: providerConfig{InterfaceType: protocol}}
		input.ReferenceImages = []providerMedia{{StorageKey: writeInlineTestImageResource(t, svc, "hosted-"+protocol+".png")}}

		policy := providerMediaHydrationPolicyFor(ctx, input)
		if !policy.requireURL {
			t.Fatalf("%s: 云端 requireURL 被意外放宽: %#v", protocol, policy)
		}
		_ = svc.hydrateGenerationMedia("user-1", &input, policy)
		if strings.HasPrefix(input.ReferenceImages[0].DataURL, "data:") {
			t.Fatalf("%s: 云端模式不应内联本地素材", protocol)
		}
	}
}

// 放宽只针对 Agnes 视频协议。newapi / minimax / volcengine 等远程渠道在本地模式下
// 依然必须走公网 URL，不能被顺带放开。
func TestInlineRelaxationDoesNotLeakToRemoteProtocols(t *testing.T) {
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	// 这些协议按插件声明要求公网素材地址。注意 "newapi" 不在列表里：它的 multipart
	// 合同本来就接受内联（见 TestInstalledMediaContractOverridesLegacyGuess）。
	// 注意 "newapi" 与 "agnes-image" 都不在列表里：它们按插件声明本来就接受内联，
	// 属于既有能力，不是本次放开的范围。
	protocols := []string{
		string(model.ChannelInterfaceNewAPIChannel2),
		string(model.ChannelInterfaceMiniMaxVideo),
		string(model.ChannelInterfaceVolcengineArkVideo),
	}
	for _, protocol := range protocols {
		if acceptsInlineMediaInLocalMode(protocol) {
			t.Fatalf("%s: 不应被纳入本地内联放行名单", protocol)
		}
		// 这些协议必须仍然要求某种非内联的素材传输方式：要么公网 URL，要么 HTTPS 地址。
		// （volcengine-ark-video 走 preferHTTPS 而非 requireURL，所以不能只查 requireURL。）
		policy := providerMediaHydrationPolicyFor(ctx, canvasGenerationInput{Config: providerConfig{InterfaceType: protocol}})
		if !policy.requireURL && !policy.preferURL && !policy.preferHTTPS {
			t.Fatalf("%s: 素材传输策略被意外放宽成内联: %#v", protocol, policy)
		}
	}
}

// 调用方经常同时带上一个本地回环地址（例如 /api/resources/<id>/file）。声明式插件从
// media.value 取素材，而 value 的解析顺序是「先 URL 后 dataUrl」——不把 URL 清空的话，
// 内联出来的 data URL 会被这个上游拉不到的死地址盖掉，表现为上游一直等图直到超时。
func TestAgnesVideoInlineOverridesLoopbackURL(t *testing.T) {
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	svc := newResourceTestService(t)
	svc.mode = serviceModeLocal
	svc.localResourceStorage = true
	input := canvasGenerationInput{Mode: "video", Config: providerConfig{InterfaceType: "agnes-video"}}
	input.ReferenceImages = []providerMedia{{
		StorageKey: writeInlineTestImageResource(t, svc, "inline-with-url.png"),
		URL:        "/api/resources/inline-with-url.png/file",
	}}

	policy := providerMediaHydrationPolicyFor(ctx, input)
	if err := svc.hydrateGenerationMedia("user-1", &input, policy); err != nil {
		t.Fatalf("内联失败: %v", err)
	}
	got := input.ReferenceImages[0]
	if !strings.HasPrefix(got.DataURL, "data:image/png;base64,") {
		t.Fatalf("期望内联 data URL，实际 %q", got.DataURL)
	}
	if strings.TrimSpace(got.URL) != "" {
		t.Fatalf("内联优先时回环 URL 必须被清空，实际仍为 %q", got.URL)
	}
}

// 内联要有体积上限，超限必须返回明确错误，不能静默截断后把一张坏图发给上游。
func TestInlineMediaBudgetRejectsOversizedPayload(t *testing.T) {
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	svc := newResourceTestService(t)
	svc.mode = serviceModeLocal
	svc.localResourceStorage = true
	input := canvasGenerationInput{Mode: "video", Config: providerConfig{InterfaceType: "agnes-video"}}
	input.ReferenceImages = []providerMedia{{StorageKey: writeInlineTestImageResource(t, svc, "inline-budget.png")}}

	policy := providerMediaHydrationPolicyFor(ctx, input)
	if err := svc.hydrateGenerationMedia("user-1", &input, policy); err != nil {
		t.Fatalf("基线内联失败: %v", err)
	}
	input.ReferenceImages[0].DataURL = "data:image/png;base64," + strings.Repeat("A", maxInlineMediaDataURLBytes+1)
	if err := validateInlineMediaBudget(&input); err == nil {
		t.Fatal("超出内联体积上限时应返回错误")
	}
	input.ReferenceImages[0].DataURL = "data:image/png;base64,AAAA"
	if err := validateInlineMediaBudget(&input); err != nil {
		t.Fatalf("体积在上限内不应报错: %v", err)
	}
}

func TestAcceptsInlineMediaInLocalModeScope(t *testing.T) {
	for _, protocol := range []string{"agnes-video", "agnes-video-v20"} {
		if !acceptsInlineMediaInLocalMode(protocol) {
			t.Fatalf("%s: 应支持本地内联", protocol)
		}
	}
	for _, protocol := range []string{"", "agnes-image", "agnes-video-2.5", "newapi-video", "minimax-video"} {
		if acceptsInlineMediaInLocalMode(protocol) {
			t.Fatalf("%s: 不应支持本地内联", protocol)
		}
	}
}
