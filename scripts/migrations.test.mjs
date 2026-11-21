import assert from "node:assert/strict";
import test from "node:test";
import { migrationNamesAreValid, sqlMigrationNames } from "./migrations.mjs";
import path from "node:path";

test("accepts a timestamped snake-case name", () => {
  assert.equal(migrationNamesAreValid(["20261122000000_init.sql"]), true);
});

test("rejects a name without a timestamp", () => {
  assert.equal(migrationNamesAreValid(["init.sql"]), false);
});

test("the repository migration directory is valid", async () => {
  const directory = path.join(import.meta.dirname, "..", "supabase", "migrations");
  const names = await sqlMigrationNames(directory);
  assert.equal(migrationNamesAreValid(names), true);
});
