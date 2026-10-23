package service

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/autoprovider"
	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"golang.org/x/term"
)

// sandboxCreateOpenShell maps CLI options directly to OpenShell CreateSandbox.
func (a *App) sandboxCreateOpenShell(opt SandboxCreateOpts) error {
	if opt.ExposePort < 0 || opt.ExposePort > 65535 {
		return fmt.Errorf("sandbox create: --expose port must be between 1 and 65535")
	}
	if err := validateTemplateCreateConflict(opt); err != nil {
		return err
	}
	if opt.Upload != "" && len(opt.Argv) > 0 {
		return fmt.Errorf("sandbox create: --upload cannot be combined with a trailing main command")
	}
	if strings.TrimSpace(opt.Name) == "" {
		return fmt.Errorf("sandbox create: --name is required")
	}
	if opt.ExposePort > 0 && opt.ExposeAuthorizationMode != "" && opt.ExposeAuthorizationMode != "strip" && opt.ExposeAuthorizationMode != "bearer-passthrough" {
		return fmt.Errorf("sandbox create: unsupported --expose-authorization-mode %q; choose strip or bearer-passthrough", opt.ExposeAuthorizationMode)
	}
	approvalMode := strings.ToLower(strings.TrimSpace(opt.ApprovalMode))
	if approvalMode != "" && approvalMode != "manual" && approvalMode != "auto" {
		return fmt.Errorf("sandbox create: --approval-mode must be manual|auto")
	}
	editor := strings.ToLower(strings.TrimSpace(opt.Editor))
	if editor != "" {
		if editor != "vscode" && editor != "cursor" {
			return fmt.Errorf("sandbox create: --editor must be vscode|cursor")
		}
		if len(opt.Argv) > 0 {
			return fmt.Errorf("sandbox create: --editor cannot be combined with a trailing command; use `cautem sandbox connect NAME --editor %s` after the sandbox is ready", editor)
		}
		if opt.ExposePort > 0 || len(opt.Forwards) > 0 {
			return fmt.Errorf("sandbox create: --editor cannot be combined with --expose or --forward")
		}
	}
	if opt.Template != "" && (opt.CPU != 0 || opt.Memory != "" || opt.GPU || opt.GPUCount != 0 || opt.Env != nil || opt.DriverConfigJSON != "") {
		return fmt.Errorf("sandbox create: inline workload options cannot override a shared template")
	}
	image := ""
	rootfsTarPath := ""
	if opt.Template == "" {
		if path, isTar, sourceErr := resolveRootfsTarSource(opt.From); sourceErr != nil {
			return fmt.Errorf("sandbox create --from: %w", sourceErr)
		} else if isTar {
			if opt.Image != "" {
				return fmt.Errorf("sandbox create: a rootfs tar --from source cannot be combined with --image")
			}
			rootfsTarPath = path
		} else {
			var err error
			image, err = gwconfig.ResolveImage(opt.From, opt.Image)
			if err != nil {
				return fmt.Errorf("sandbox create source: %w", err)
			}
		}
		if image == "" && rootfsTarPath == "" {
			return fmt.Errorf("sandbox create: provide --image, --from, or a shared --template")
		}
		if rootfsTarPath == "" && opt.From != "" && strings.HasPrefix(image, "cautem-sandbox:") {
			return fmt.Errorf("sandbox create: --from %q resolves to a local-only image; publish it, set CAUTEM_USE_GHCR=1, or pass a remotely pullable --image", opt.From)
		}
	}
	var cpuMillis uint32
	if math.IsNaN(opt.CPU) || math.IsInf(opt.CPU, 0) || opt.CPU < 0 {
		return fmt.Errorf("sandbox create: CPU must be a finite non-negative quantity")
	}
	if opt.CPU > 0 {
		milli := opt.CPU * 1000
		if math.IsNaN(milli) || math.IsInf(milli, 0) || milli > 1_000_000 || math.Abs(milli-math.Round(milli)) > 1e-7 {
			return fmt.Errorf("sandbox create: CPU must resolve to a whole number of millicores")
		}
		cpuMillis = uint32(math.Round(milli))
	}
	var memoryBytes uint64
	if opt.Memory != "" {
		parsed, parseErr := ParseMemoryQuantity(opt.Memory)
		if parseErr != nil || parsed <= 0 || parsed > 1<<50 {
			if parseErr == nil {
				parseErr = fmt.Errorf("must be positive and no greater than 1 PiB")
			}
			return fmt.Errorf("sandbox create --memory: %w", parseErr)
		}
		memoryBytes = uint64(parsed)
	}
	if opt.GPUCount < 0 || opt.GPUCount > 1024 {
		return fmt.Errorf("sandbox create: GPU count must be between 0 and 1024")
	}
	for _, port := range opt.Forwards {
		if port < 1 || port > 65535 {
			return fmt.Errorf("sandbox create: forward port must be between 1 and 65535")
		}
		listener, listenErr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if listenErr != nil {
			return fmt.Errorf("sandbox create: forward port %d is unavailable: %w", port, listenErr)
		}
		_ = listener.Close()
	}
	preparedUpload := ""
	if opt.Upload != "" {
		staged, cleanup, filtered, uploadErr := prepareGitAwareUpload(a.CommandContext(), opt.Upload, !opt.NoGitIgnore)
		if uploadErr != nil {
			return fmt.Errorf("sandbox create --upload preflight: %w", uploadErr)
		}
		defer cleanup()
		preparedUpload = staged
		if filtered {
			fmt.Fprintf(os.Stderr, "sandbox create --upload: applying .gitignore rules to %s\n", opt.Upload)
		} else if !opt.NoGitIgnore {
			fmt.Fprintf(os.Stderr, "sandbox create --upload: Git filtering was not applied to %s (outside a Git work tree or symlink source)\n", opt.Upload)
		}
	}
	if err := validateSandboxCreateResources(opt); err != nil {
		return fmt.Errorf("sandbox create: %w", err)
	}
	gpuCount := uint32(opt.GPUCount)
	if raw := strings.TrimSpace(opt.DriverConfigJSON); raw != "" {
		var config map[string]any
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
			return fmt.Errorf("sandbox create: --driver-config-json must be a JSON object keyed by compute driver")
		}
	}
	url, err := a.currentGatewayURL()
	if err != nil {
		return fmt.Errorf("sandbox create: %w", err)
	}
	if url == "" {
		return fmt.Errorf("sandbox create: no Gateway selected; use cautem gateway select")
	}
	workspace := strings.TrimSpace(opt.GatewayWorkspace)
	if workspace == "" {
		workspace = strings.TrimSpace(a.GlobalWorkspace)
	}
	if workspace == "" {
		workspace = "default"
	}
	client := a.clientFor(url)
	defer client.Close()
	client.Workspace = workspace
	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()
	driverConfigJSON := opt.DriverConfigJSON
	if rootfsTarPath != "" {
		if !isLoopbackGatewayEndpoint(url) {
			return fmt.Errorf("sandbox create: local rootfs tar sources require a Gateway on this machine")
		}
		file, openErr := os.Open(rootfsTarPath)
		if openErr != nil {
			return fmt.Errorf("sandbox create --from %q: %w", rootfsTarPath, openErr)
		}
		fileInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return fmt.Errorf("sandbox create --from %q: %w", rootfsTarPath, statErr)
		}
		token, uploadErr := client.UploadRootfsTar(ctx, filepath.Base(rootfsTarPath), uint64(fileInfo.Size()), file)
		closeErr := file.Close()
		if uploadErr != nil {
			return fmt.Errorf("sandbox create rootfs staging: %w", uploadErr)
		}
		if closeErr != nil {
			return fmt.Errorf("sandbox create rootfs source close: %w", closeErr)
		}
		driverConfigJSON, err = mergeRootfsTarDriverConfig(driverConfigJSON, token)
		if err != nil {
			return err
		}
	}
	providerMode := autoprovider.ModeOff
	switch {
	case opt.NoAutoProviders:
	case opt.AutoProviders:
		providerMode = autoprovider.ModeOn
	default:
		if len(InferProvidersFromArgv(opt.Argv)) > 0 {
			providerMode = autoprovider.ModeOn
		}
	}
	providerNames := autoprovider.Merge(opt.Providers, InferProvidersFromArgv(opt.Argv), providerMode)
	attachedProviders := make([]string, 0, len(providerNames))
	for _, name := range providerNames {
		_, _, instance, providerErr := a.resolveProviderForCreateInWorkspace(name, url, workspace)
		if providerErr != nil {
			return fmt.Errorf("sandbox create provider %q: %w", name, providerErr)
		}
		attachedProviders = append(attachedProviders, instance)
	}
	mainTTY := opt.ForceTTY || (term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())))
	awaitMainProcess := len(opt.Argv) > 0 && !opt.Detach && editor == "" && mainTTY
	sandbox := klorq.Sandbox{
		Name: opt.Name, Workspace: workspace, Image: image,
		WorkloadTemplateName: opt.Template, Labels: opt.Labels,
		CPUMillis: cpuMillis, MemoryBytes: memoryBytes, GPUCount: gpuCount, GPURequested: opt.GPU || envTruthy("CAUTEM_GPU"),
		Environment: opt.Env, DriverConfigJSON: driverConfigJSON,
		AttachedProviders: attachedProviders, RestartPolicy: opt.RestartPolicy,
		// OpenShell forces retention when a forward or service exposure needs
		// to outlive the create request, even when --no-keep was supplied.
		Ephemeral: opt.NoKeep && opt.ExposePort == 0 && len(opt.Forwards) == 0,
		TTY:       mainTTY, AwaitMainProcessAttachment: awaitMainProcess,
	}
	if strings.TrimSpace(opt.Policy) != "" {
		policyDocument, readErr := os.ReadFile(opt.Policy)
		if readErr != nil {
			return fmt.Errorf("sandbox create --policy: %w", readErr)
		}
		sandbox.PolicyYAML = string(policyDocument)
	}
	if opt.ExposePort > 0 {
		mode := klorq.ServiceAuthorizationModeStrip
		if opt.ExposeAuthorizationMode == "bearer-passthrough" {
			mode = klorq.ServiceAuthorizationModeBearerPassthrough
		}
		sandbox.ServiceExposures = []klorq.ServiceExposure{{
			Name: opt.Name, TargetPort: uint32(opt.ExposePort), AuthorizationMode: mode,
		}}
	}
	createCommand := canonicalMainCommand(opt.Argv)
	if len(createCommand) > 0 && !opt.Detach && !awaitMainProcess {
		// The retained main-process attach contract is currently PTY-backed.
		createCommand = nil
	}
	createdResult, err := client.CreateControlSandboxOperation(ctx, sandbox, createCommand)
	if err != nil {
		return err
	}
	created := createdResult.Sandbox
	a.rememberLastSandbox(created.Name, url)
	if preparedUpload != "" {
		destination, destinationErr := sandboxCreateUploadDestination(preparedUpload, opt.Upload)
		if destinationErr != nil {
			return fmt.Errorf("sandbox create --upload preflight: %w", destinationErr)
		}
		if uploadErr := uploadGatewayPath(ctx, client, workspace, created.Name, preparedUpload, destination, TimeoutWork); uploadErr != nil {
			return fmt.Errorf("sandbox %q was created, but upload failed; retry with `cautem sandbox upload %s %s`: %w", created.Name, opt.Upload, created.Name+":"+destination, uploadErr)
		}
	}
	for _, port := range opt.Forwards {
		if forwardErr := a.ForwardStart(created.Name, strconv.Itoa(port), "", true); forwardErr != nil {
			return fmt.Errorf("sandbox %q was created, but forward port %d failed: %w", created.Name, port, forwardErr)
		}
	}
	if approvalMode != "" && approvalMode != "manual" {
		if settingErr := client.PutSandboxSetting(ctx, workspace, created.Name, "proposal_approval_mode", approvalMode); settingErr != nil {
			fmt.Fprintf(os.Stderr, "warning: sandbox created but approval mode could not be set: %v\n  retry with: cautem settings set %s --key proposal_approval_mode --value %s\n", settingErr, created.Name, approvalMode)
		}
	}
	if editor != "" {
		return a.SandboxConnect(ConnectOpts{Name: created.Name, Editor: editor})
	}
	if opt.ExposePort > 0 {
		serviceURL := createdResult.ServiceURLs[created.Name]
		if serviceURL == "" {
			return fmt.Errorf("sandbox create: sandbox %s was created, but Gateway did not return its service URL; retry with `cautem service expose %s %d`", created.Name, created.Name, opt.ExposePort)
		}
		modeLabel := "strip"
		if opt.ExposeAuthorizationMode == "bearer-passthrough" {
			modeLabel = "bearer-passthrough"
		}
		fmt.Printf("sandbox service exposed: %s:%d url=%s (authorization mode: %s)\n", created.Name, opt.ExposePort, serviceURL, modeLabel)
	}
	fmt.Printf("sandbox create: ok name=%s id=%s workspace=%s runtime=gateway image=%s\n",
		created.Name, shortID(created.ID), created.Workspace, created.Image)
	if len(opt.Argv) > 0 && !opt.Detach {
		argv := canonicalMainCommand(opt.Argv)
		if awaitMainProcess {
			argv = nil
		}
		code, execErr := runGatewayInteractive(ctx, client, workspace, ExecOpts{Name: created.Name, Argv: argv, TTY: mainTTY, AttachMainProcess: awaitMainProcess}, klorq.SandboxExecOptions{AttachMainProcess: awaitMainProcess})
		if execErr != nil {
			err := execErr
			return fmt.Errorf("sandbox create: sandbox was created; native interactive command failed: %w", err)
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
	}
	return nil
}
