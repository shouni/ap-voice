package domain

// WorkerTaskPath is the path on which the worker receives tasks.
// Both the enqueuing side (tasks.Config.WorkerPath) and the receiving side (route registration in internal/server) use it.
// With two copies, changing only one would still build, and every enqueued task would 404.
// gcp-kit's tasks package joins it onto the service URL and normalises it.
const WorkerTaskPath = "/tasks/generate"
