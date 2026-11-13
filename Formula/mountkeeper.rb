class Mountkeeper < Formula
  desc "Native macOS SMB network share manager"
  homepage "https://github.com/sanriodev/mountkeeper"
  # url and sha256 are rewritten by .github/workflows/release.yml on every
  # tagged release. The checksum below is a placeholder until v0.1.0 exists.
  url "https://github.com/sanriodev/mountkeeper/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
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
      Shares with auto-connect enabled are mounted by a per-user LaunchAgent.
      Before uninstalling, disable it with:
        mountkeeper autoconnect <share> false
    EOS
  end

  test do
    assert_match "mountkeeper", shell_output("#{bin}/mountkeeper --help")
    assert_match version.to_s, shell_output("#{bin}/mountkeeper --version")

    ENV["MOUNTKEEPER_CONFIG"] = (testpath/"config.yaml").to_s
    system bin/"mountkeeper", "add", "smb://nas.example.com/media", "--yes"
    assert_match "nas.example.com", shell_output("#{bin}/mountkeeper list")
    assert_match "Disconnected", shell_output("#{bin}/mountkeeper status")
  end
end
