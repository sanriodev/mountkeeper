class Mountkeeper < Formula
  desc "Native macOS SMB share manager with Keychain and Kerberos support"
  homepage "https://github.com/sanriodev/mountkeeper"
  # url and sha256 are rewritten by .github/workflows/release.yml on every
  # tagged release. The checksum below is a placeholder until v0.1.0 exists.
  url "https://github.com/sanriodev/mountkeeper/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "d751b64c53357892d77b51b286dd6bf3e0ed8ea3b9c9a3abf117a5ea7d193b5d"
  license "MIT"
  head "https://github.com/sanriodev/mountkeeper.git", branch: "main"

  depends_on "go" => :build
  depends_on :macos

  def install
    # cgo is required: Keychain and mounting use the Security and NetFS frameworks.
    ENV["CGO_ENABLED"] = "1"
    system "go", "build",
           *std_go_args(ldflags: "-s -w -X main.version=#{version}"),
           "./cmd/mountkeeper"
    generate_completions_from_executable(bin/"mountkeeper", "completion")
  end

  def caveats
    <<~EOS
      Run "mountkeeper" without arguments for the interactive interface.

      Automatic connecting uses a per-user LaunchAgent. After an uninstall it
      removes itself within a few minutes. Your settings in
      ~/.config/mountkeeper and the passwords in the Keychain (service
      "mountkeeper") are kept.
    EOS
  end

  test do
    assert_match "mountkeeper", shell_output("#{bin}/mountkeeper --help")
    assert_match version.to_s, shell_output("#{bin}/mountkeeper --version")

    ENV["MOUNTKEEPER_CONFIG"] = (testpath/"config.yaml").to_s
    system bin/"mountkeeper", "add", "smb://nas.example.com/media", "--yes"
    assert_match "nas.example.com", shell_output("#{bin}/mountkeeper list")
    assert_match "Disconnected", shell_output("#{bin}/mountkeeper status --no-check")
  end
end
