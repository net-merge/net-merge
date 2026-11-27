{
  lib,
  buildGoModule,
  go_1_27,
  version ? "dev",
}:

(buildGoModule.override { go = go_1_27; }) {
  pname = "net-merge";
  inherit version;

  src = lib.fileset.toSource {
    root = ./..;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
    ];
  };

  vendorHash = "sha256-pPUwE31orUbyS7NkomRz91e1uzKAQNh0IoL9Z75Raj4=";
  subPackages = [ "cmd/net-merge" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  # The tests bind to loopback aliases and connect to local listeners; the sandbox allows that.
  doCheck = true;

  meta = {
    description = "Use all your network adapters at once: balances connections across Wi-Fi, Ethernet and tethers";
    homepage = "https://github.com/net-merge/net-merge";
    license = lib.licenses.mit;
    mainProgram = "net-merge";
    platforms = lib.platforms.linux ++ lib.platforms.windows;
  };
}
