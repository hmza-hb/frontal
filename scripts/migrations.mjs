import { readdir } from "node:fs/promises";
import path from "node:path";

const migrationName = /^[0-9]{14}_[a-z0-9_]+\.sql$/;

export function migrationNamesAreValid(names) {
  return names.every((name) => migrationName.test(name));
}

export async function sqlMigrationNames(directory) {
  const entries = await readdir(directory);
  return entries.filter((name) => name.endsWith(".sql"));
}

async function main() {
  const directory = path.join(import.meta.dirname, "..", "supabase", "migrations");
  const names = await sqlMigrationNames(directory);
  if (!migrationNamesAreValid(names)) {
    process.stderr.write("Migration file names must match YYYYMMDDHHMMSS_short_name.sql\n");
    process.exitCode = 1;
  }
}

if (process.argv[1] && import.meta.url.endsWith(path.basename(process.argv[1]))) {
  await main();
}
