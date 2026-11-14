{
  description = "Dagu development shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      # Only what the Makefile expects to find on the host. golangci-lint,
      # gotestsum, oapi-codegen and the protoc plugins are pinned in go.mod and
      # installed into .local/bin by their make targets; nixpkgs versions would
      # drift from what CI runs.
      devShells = forEachSystem (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go_1_27
            pkgs.nodejs_22
            pkgs.pnpm_10
            pkgs.gnumake
            # Used by make proto and make certs.
            pkgs.curl
            pkgs.unzip
            pkgs.openssl
            # Used by test steps.
            pkgs.git
            pkgs.perl
          ];
        };
      });

      formatter = forEachSystem (pkgs: pkgs.nixfmt-tree);
    };
}
