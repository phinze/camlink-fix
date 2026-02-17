self:
{
  config,
  lib,
  pkgs,
  ...
}:

with lib;
let
  cfg = config.services.camlink-fix;
in
{
  options.services.camlink-fix = {
    enable = mkEnableOption "Cam Link 4K auto-fix daemon";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.system}.default;
      defaultText = literalExpression "camlink-fix.packages.\${system}.default";
      description = "The camlink-fix package to use.";
    };

    deviceName = mkOption {
      type = types.str;
      default = "Cam Link 4K";
      description = "Name of the camera device as it appears in system_profiler.";
    };

    user = mkOption {
      type = types.str;
      default = "phinze";
      description = "User account that will run the daemon.";
    };

    notify = mkOption {
      type = types.bool;
      default = true;
      description = "Show macOS notifications when fixing the camera.";
    };

    wakeDelay = mkOption {
      type = types.int;
      default = 5;
      description = "Seconds to wait after wake before checking the camera.";
    };
  };

  config = mkIf cfg.enable {
    # uhubctl and ffmpeg are runtime dependencies
    environment.systemPackages = [
      pkgs.ffmpeg
      pkgs.uhubctl
      cfg.package
    ];

    # Sudoers rule for passwordless uhubctl
    security.sudo.extraConfig = ''
      ${cfg.user} ALL=(ALL) NOPASSWD: ${pkgs.uhubctl}/bin/uhubctl
    '';

    # Launchd user agent running the Go daemon
    launchd.user.agents.camlink-fix = {
      path = [ "/usr/bin" "/bin" "/usr/sbin" "/sbin" ];
      serviceConfig = {
        ProgramArguments = [
          "${cfg.package}/bin/camlink-fix"
          "--uhubctl-path" "${pkgs.uhubctl}/bin/uhubctl"
          "--ffmpeg-path" "${pkgs.ffmpeg}/bin/ffmpeg"
          "--device-name" cfg.deviceName
          "--wake-delay" "${toString cfg.wakeDelay}s"
          "--notify=${boolToString cfg.notify}"
        ];
        KeepAlive = true;
        RunAtLoad = true;
        StandardOutPath = "/tmp/camlink-fix.log";
        StandardErrorPath = "/tmp/camlink-fix.log";
      };
    };
  };
}
