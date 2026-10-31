import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import { customSqlProblem, SCHEMA_RE, utf8Bytes } from "./schema-version-model";
import { emptyRuns, JOB_ID, jobDetail, noTestRestores, stepUpHandlers } from "./test-fixtures";

const presets = {
  schemaVersionPresets: [
    {
      __typename: "SchemaVersionPreset",
      name: "golang-migrate",
      label: "golang-migrate",
      description: "The version in schema_migrations, marked (dirty) after a failed migration.",
      postgresTable: "schema_migrations",
      mysqlTable: "schema_migrations",
      engines: ["POSTGRES", "MYSQL", "MARIADB"],
    },
    {
      __typename: "SchemaVersionPreset",
      name: "flyway",
      label: "Flyway",
      description: "The version of the newest successful versioned migration.",
      postgresTable: "flyway_schema_history",
      mysqlTable: "flyway_schema_history",
      engines: ["POSTGRES", "MYSQL", "MARIADB"],
    },
  ],
};

const editorHandlers = () => ({
  JobDetail: () => ok({ job: jobDetail() }),
  JobEditorOptions: () => ok({ connections: [], destinations: [] }),
  PreviewSchedule: () => gqlError("VALIDATION", "x"),
  UserPickerCandidates: () =>
    ok({ userDirectory: { __typename: "UserDirectoryConnection", edges: [] } }),
  TestRestoreTargets: () => ok({ connections: [] }),
  SchemaVersionPresets: () => ok(presets),
});

const saved = (sv: Record<string, unknown>) =>
  ok({
    setJobSchemaVersion: {
      __typename: "Job",
      id: JOB_ID,
      schemaVersion: { __typename: "JobSchemaVersion", ...sv },
    },
  });

describe("schema-version model", () => {
  it("checks custom queries and schema names like the server", () => {
    expect(customSqlProblem("SELECT 1")).toBeNull();
    expect(customSqlProblem("  with v as (select 1) select * from v")).toBeNull();
    expect(customSqlProblem("SELECT 1;")).toMatch(/semicolon/);
    expect(customSqlProblem("SELECT 1 -- newest")).toMatch(/comment \(--\)/);
    expect(customSqlProblem("/* x */ SELECT 1")).toMatch(/comment/);
    expect(customSqlProblem("SELECT 1 # x")).toMatch(/comment \(#\)/);
    expect(customSqlProblem("SELECT 1 -- \r, pg_read_file('/etc/hostname')\n")).toMatch(
      /carriage return/,
    );
    expect(customSqlProblem("SELECT $1")).toMatch(/dollar/);
    expect(customSqlProblem("SELECT u&'x'")).toMatch(/U& escape/);
    expect(customSqlProblem("selected FROM t")).toMatch(/SELECT or WITH/);
    expect(customSqlProblem("  ")).toMatch(/Enter a query/);
    expect(customSqlProblem("DELETE FROM t")).toMatch(/SELECT or WITH/);
    expect(customSqlProblem(`SELECT '${"é".repeat(1100)}'`)).toMatch(/longer than 2048 bytes/);
    expect(utf8Bytes("é")).toBe(2);
    expect(SCHEMA_RE.test("public")).toBe(true);
    expect(SCHEMA_RE.test("1x")).toBe(false);
  });
});

describe("schema-version settings", () => {
  it("saves a framework preset with its schema without a step-up", async () => {
    const sent: Vars[] = [];
    const { user } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers(),
      SetJobSchemaVersion: (vars) => {
        sent.push(vars);
        return saved({
          mode: "PRESET",
          preset: "flyway",
          presetLabel: "Flyway",
          schema: "app",
          customSql: null,
        });
      },
    });
    const form = await screen.findByRole("form", { name: "Schema-version settings" });
    await user.click(within(form).getByRole("radio", { name: /Framework preset/ }));
    const select = within(form).getByRole("combobox", { name: "Migration framework" });
    await within(form).findByRole("option", { name: "Flyway" });
    await user.selectOptions(select, "flyway");
    expect(within(form).getByText("Reads flyway_schema_history")).toBeInTheDocument();
    const schema = within(form).getByRole("textbox", { name: "Schema" });
    await user.clear(schema);
    await user.type(schema, "app");
    await user.click(within(form).getByRole("button", { name: "Save schema version" }));
    expect(await screen.findByText("Schema-version capture saved.")).toBeInTheDocument();
    expect(sent).toEqual([
      { jobId: JOB_ID, input: { mode: "PRESET", preset: "flyway", schema: "app" } },
    ]);
  });

  it("warns about custom queries, checks them and asks for a step-up", async () => {
    let stepped = false;
    const sent: Vars[] = [];
    const { user } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers(),
      ...stepUpHandlers(() => {
        stepped = true;
      }),
      SetJobSchemaVersion: (vars) => {
        sent.push(vars);
        if (!stepped) return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        return saved({
          mode: "CUSTOM",
          preset: null,
          presetLabel: null,
          schema: "public",
          customSql: "SELECT 1",
        });
      },
    });
    const form = await screen.findByRole("form", { name: "Schema-version settings" });
    await user.click(within(form).getByRole("radio", { name: /Custom query/ }));
    expect(within(form).getByRole("note")).toHaveTextContent(/runs on your databases/);
    const sql = within(form).getByRole("textbox", { name: "Version query" });
    expect(within(form).getByText(/UNION, INTERSECT, EXCEPT/)).toBeInTheDocument();
    await user.type(sql, "DELETE FROM t");
    expect(within(form).getByText("The query must start with SELECT or WITH.")).toBeInTheDocument();
    await user.clear(sql);
    await user.type(sql, "SELECT max(version) FROM public.schema_migrations");
    await user.click(within(form).getByRole("button", { name: "Save schema version" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Schema-version capture saved.")).toBeInTheDocument();
    expect(sent).toHaveLength(2);
    expect(sent[1]).toEqual({
      jobId: JOB_ID,
      input: { mode: "CUSTOM", customSql: "SELECT max(version) FROM public.schema_migrations" },
    });
  });
});

describe("job page", () => {
  it("shows the capture mode and the last run's version per database", async () => {
    const base = jobDetail();
    const job = {
      ...base,
      schemaVersion: {
        ...base.schemaVersion,
        mode: "PRESET",
        preset: "golang-migrate",
        presetLabel: "golang-migrate",
      },
      lastRun: {
        ...base.lastRun,
        items: [
          {
            __typename: "RunItem",
            id: "i1",
            kind: "DATABASE",
            database: "billing",
            position: 0,
            schemaVersion: "42 (dirty)",
            schemaVersionStatus: "OK",
          },
          {
            __typename: "RunItem",
            id: "i2",
            kind: "DATABASE",
            database: "ledger",
            position: 1,
            schemaVersion: null,
            schemaVersionStatus: "NONE",
          },
          {
            __typename: "RunItem",
            id: "i3",
            kind: "GLOBALS",
            database: null,
            position: 2,
            schemaVersion: null,
            schemaVersionStatus: null,
          },
        ],
      },
    };
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      JobDetail: () => ok({ job }),
      RunsList: () => ok(emptyRuns),
      LatestTestRestore: () => ok(noTestRestores),
    });
    const list = await screen.findByRole("list", { name: "Last run" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent(/billing.*42 \(dirty\)/);
    expect(rows[1]).toHaveTextContent(/ledger.*No version/);
    expect(screen.getAllByText("golang-migrate").length).toBeGreaterThan(0);
  });

  it("explains a missing version after a finished run", async () => {
    const base = jobDetail();
    const job = {
      ...base,
      schemaVersion: { ...base.schemaVersion, mode: "CUSTOM", preset: null, presetLabel: null },
      lastRun: { ...base.lastRun, status: "FAILED", items: [] },
    };
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      JobDetail: () => ok({ job }),
      RunsList: () => ok(emptyRuns),
      LatestTestRestore: () => ok(noTestRestores),
    });
    expect(
      await screen.findByText(
        /The last run recorded no version: only complete database dumps record one/,
      ),
    ).toBeInTheDocument();
  });
});
