const std = @import("std");

pub fn main() !void {
    var args = std.process.args();
    _ = args.next();
    const mode = args.next() orelse {
        keepAlive();
    };
    if (!std.mem.eql(u8, mode, "signal-token")) {
        std.process.exit(2);
    }

    const signal_name = args.next() orelse std.process.exit(2);
    const token = args.next() orelse std.process.exit(2);
    if (args.next() != null) {
        std.process.exit(2);
    }

    const signal: u8 = if (std.mem.eql(u8, signal_name, "TERM"))
        std.posix.SIG.TERM
    else if (std.mem.eql(u8, signal_name, "KILL"))
        std.posix.SIG.KILL
    else
        std.process.exit(2);
    try signalToken(std.heap.page_allocator, token, signal);
}

fn keepAlive() noreturn {
    while (true) {
        std.time.sleep(100 * std.time.ns_per_ms);
    }
}

fn signalToken(allocator: std.mem.Allocator, token: []const u8, signal: u8) !void {
    const needle = try std.fmt.allocPrint(allocator, "DAGU_EXEC_TOKEN={s}", .{token});
    defer allocator.free(needle);

    var proc = try std.fs.openDirAbsolute("/proc", .{ .iterate = true });
    defer proc.close();
    var entries = proc.iterate();
    while (try entries.next()) |entry| {
        if (entry.kind != .directory) continue;
        const pid = std.fmt.parseInt(std.posix.pid_t, entry.name, 10) catch continue;
        if (pid <= 1) continue;
        if (!try processHasToken(allocator, pid, needle)) continue;

        killDescendants(allocator, pid, signal, 0);
        std.posix.kill(-pid, signal) catch {};
        std.posix.kill(pid, signal) catch {};
    }
}

fn processHasToken(allocator: std.mem.Allocator, pid: std.posix.pid_t, needle: []const u8) !bool {
    var path_buffer: [std.fs.max_path_bytes]u8 = undefined;
    const path = std.fmt.bufPrint(&path_buffer, "/proc/{d}/environ", .{pid}) catch return false;
    const file = std.fs.openFileAbsolute(path, .{}) catch return false;
    defer file.close();
    const environ = file.readToEndAlloc(allocator, 1024 * 1024) catch return false;
    defer allocator.free(environ);

    var values = std.mem.splitScalar(u8, environ, 0);
    while (values.next()) |value| {
        if (std.mem.eql(u8, value, needle)) return true;
    }
    return false;
}

fn killDescendants(
    allocator: std.mem.Allocator,
    pid: std.posix.pid_t,
    signal: u8,
    depth: u8,
) void {
    if (depth >= 64) return;

    var path_buffer: [std.fs.max_path_bytes]u8 = undefined;
    const path = std.fmt.bufPrint(&path_buffer, "/proc/{d}/task/{d}/children", .{ pid, pid }) catch return;
    const file = std.fs.openFileAbsolute(path, .{}) catch return;
    defer file.close();
    const children = file.readToEndAlloc(allocator, 1024 * 1024) catch return;
    defer allocator.free(children);

    var values = std.mem.tokenizeAny(u8, children, " \t\r\n");
    while (values.next()) |value| {
        const child = std.fmt.parseInt(std.posix.pid_t, value, 10) catch continue;
        if (child <= 1) continue;
        killDescendants(allocator, child, signal, depth + 1);
        std.posix.kill(child, signal) catch {};
    }
}
