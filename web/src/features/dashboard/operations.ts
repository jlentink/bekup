import { graphql } from "@/graphql/gen";

/** Every visible job (backup owners: their own, filtered by the server). */
export const DashboardJobsQuery = graphql(`
  query DashboardJobs($first: Int) {
    jobs(first: $first) {
      edges {
        node {
          id
          name
          enabled
          nextRunAt
          scheduleDescription
          missed
          missedSince
          connection {
            id
            name
            deleted
          }
          encryption {
            configured
            mode
            optedOut
          }
          lastRun {
            id
            status
            queuedAt
            finishedAt
            warnings {
              code
            }
          }
        }
      }
      pageInfo {
        hasNextPage
      }
    }
  }
`);

/** Active runs and the failures of the last 7 days, in one request. */
export const DashboardRunsQuery = graphql(`
  query DashboardRuns($since: Time!) {
    running: runs(filter: { status: RUNNING }, first: 20) {
      edges {
        node {
          id
          status
          trigger
          queuedAt
          startedAt
          finishedAt
          errorCode
          errorMessage
          job {
            id
            name
          }
        }
      }
    }
    queued: runs(filter: { status: QUEUED }, first: 20) {
      edges {
        node {
          id
          status
          trigger
          queuedAt
          startedAt
          finishedAt
          errorCode
          errorMessage
          job {
            id
            name
          }
        }
      }
    }
    failed: runs(filter: { status: FAILED, since: $since }, first: 10) {
      edges {
        node {
          id
          status
          trigger
          queuedAt
          startedAt
          finishedAt
          errorCode
          errorMessage
          job {
            id
            name
          }
        }
      }
      pageInfo {
        hasNextPage
      }
    }
  }
`);

/** Newest restores at load; changes arrive on DashboardActivity. */
export const DashboardRestoresQuery = graphql(`
  query DashboardRestores {
    restores(first: 20) {
      edges {
        node {
          id
          status
          createdAt
          startedAt
          sourceDatabase
          targetDatabase
          kind
          job {
            id
            name
          }
          target {
            id
            name
          }
        }
      }
    }
  }
`);

/**
 * The page's one stream (D-197): every run and every restore the caller
 * may view, re-authorized per event by the server; shared across the tabs
 * of the browser.
 */
export const DashboardActivitySubscription = graphql(`
  subscription DashboardActivity {
    activityUpdated {
      run {
        id
        status
        warnings {
          code
        }
        trigger
        queuedAt
        startedAt
        finishedAt
        errorCode
        errorMessage
        job {
          id
          name
        }
      }
      restore {
        id
        status
        createdAt
        startedAt
        sourceDatabase
        targetDatabase
        kind
        job {
          id
          name
        }
        target {
          id
          name
        }
      }
    }
  }
`);

/** Calendar slices: upcoming planned runs, and skipped occurrences in the past. */
export const DashboardCalendarQuery = graphql(`
  query DashboardCalendar($from: Time!, $to: Time!, $timezone: String!) {
    calendar(from: $from, to: $to, timezone: $timezone) {
      events {
        jobId
        jobName
        kind
        start
        status
        runId
        paused
        startedEarly
        overdue
        detail
      }
      truncated
    }
  }
`);

/** Infrastructure for the staff tiles (metadata only). */
export const DashboardInfraQuery = graphql(`
  query DashboardInfra {
    connections {
      id
      name
      tlsMode
      insecureTransport
      pinStatus
      pinExpiresAt
      lastTest {
        at
        ok
        code
        stale
        privileges {
          status
        }
      }
    }
    destinations {
      id
      name
      type
      ready
      lastTest {
        at
        ok
        code
      }
    }
  }
`);

/** Stored bytes and files per destination (staff; one server aggregate, D-196). */
export const DashboardStorageQuery = graphql(`
  query DashboardStorage {
    destinationUsage {
      destinationId
      name
      type
      storedBytes
      artifactCount
    }
  }
`);

export const DashboardSystemHealthQuery = graphql(`
  query DashboardSystemHealth {
    systemHealth {
      checkedAt
      runner {
        up
        dockerOk
        version
        activeRuns
        maxConcurrentRuns
      }
      certificates {
        component
        notAfter
      }
      queue {
        queue
        state
        jobs
      }
      earliestPinNotAfter
      oldestLastSuccess
      schedulerEnabled
      lastSchedulerTick
    }
  }
`);

export const DashboardAlertsQuery = graphql(`
  query DashboardAlerts {
    unreadNotificationCount
    notifications(unreadOnly: true, first: 5) {
      edges {
        node {
          id
          type
          severity
          title
          link
          createdAt
        }
      }
    }
  }
`);
