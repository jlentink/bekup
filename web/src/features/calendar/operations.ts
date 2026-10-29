import { graphql } from "@/graphql/gen";

export const CalendarQuery = graphql(`
  query Calendar($from: Time!, $to: Time!, $timezone: String!, $jobIds: [UUID!]) {
    calendar(from: $from, to: $to, timezone: $timezone, jobIds: $jobIds) {
      events {
        jobId
        jobName
        kind
        start
        end
        status
        trigger
        runId
        destinationId
        projected
        paused
        startedEarly
        overdue
        detail
      }
      truncated
      from
      to
      timezone
    }
  }
`);

/** The jobs for the filters (connection and destination map to job ids). */
export const CalendarJobsQuery = graphql(`
  query CalendarJobs($after: String) {
    jobs(first: 200, after: $after) {
      edges {
        node {
          id
          name
          connection {
            id
            name
          }
          destinations {
            id
            name
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/** One stream per page (graphql.stream_max_per_session); filtered on the client. */
export const CalendarRunUpdatedSubscription = graphql(`
  subscription CalendarRunUpdated {
    runUpdated {
      id
      status
      trigger
      scheduledFor
      queuedAt
      startedAt
      finishedAt
      errorCode
      job {
        id
        name
      }
    }
  }
`);
