/**
 * Typed HTTP API surface for the operator SPA (D11).
 *
 * Hand-written TypeScript types aligned with Go `/api/*` JSON shapes — **not**
 * generated from OpenAPI. Prefer importing response/request shapes from here
 * when adding call sites; runtime helpers remain in `./api`.
 *
 * Residual: optional OpenAPI / shared schema codegen later.
 */

export type {
  ActionResponse,
  ArchiveMetrics,
  ArchiveRow,
  ArchivesPayload,
  ConfigGetResponse,
  ConfigSetResponse,
  ConnectionStatus,
  Counts,
  DoctorCheck,
  DoctorReport,
  DoctorSeverity,
  HealthPayload,
  HookRow,
  HooksListResponse,
  HooksStatusResponse,
  MetricsSummary,
  RescanResponse,
  SortKey,
  StatusSnapshot,
  Toast,
  ToastKind,
  WSLInfo,
} from './types'
