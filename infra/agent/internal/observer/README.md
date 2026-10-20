# observer

Collects instance health, activity, wake schedules, and resource samples for NodeControl uplink.

## Health

GET `<instance>/_auriga/health` plus process status → ready flag.

## Activity

`ActivityTracker` keeps a 5-minute window of CPU samples (`recent_cpu_active`).

## Wake schedules

Watches `ops-cron/store.json` and `storages/schedule.json` under each instance root (poll). Parses cron with timezone (DST-aware via `time.LoadLocation` + robfig/cron). Clock is injectable.

## Resources

- Linux: cgroup v2 (`CgroupCollector`)
- Windows: stub (`WindowsCollector`) until Job Object stats land with the runtime

## Reporting

`Reporter` receives `WakeSchedule` / `NodeUsage` / ready updates. App currently uses `LogReporter`; wiring into `conn.Session` send path is left for the lead (conn is outside A3 edit scope).
