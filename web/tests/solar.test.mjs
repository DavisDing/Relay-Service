import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import ts from 'typescript';
const code = ts.transpileModule(readFileSync(new URL('../src/solar.ts',import.meta.url),'utf8'), {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText;
const {solarTheme,validLocation} = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('solar theme follows coordinates, seasons and polar day/night, independent of browser timezone', () => {
 const shanghai={latitude:31.23,longitude:121.47};
 for(const [date,expected] of [
  ['2026-06-21T04:00:00Z','light'], ['2026-06-21T16:00:00Z','dark'],
  ['2026-06-21T21:10:00Z','light'], ['2026-12-21T21:10:00Z','dark'],
  ['2026-10-28T21:45:00Z','dark'], ['2026-10-28T22:30:00Z','light'],
 ]) assert.equal(solarTheme(new Date(date),shanghai),expected,date);
 assert.equal(solarTheme(new Date('2026-06-21T00:00:00Z'),{latitude:80,longitude:0}),'light');
 assert.equal(solarTheme(new Date('2026-12-21T12:00:00Z'),{latitude:80,longitude:0}),'dark');
 assert.equal(solarTheme(new Date('2026-06-21T00:00:00Z'),{latitude:-80,longitude:0}),'dark');
 assert.equal(solarTheme(new Date('2026-06-21T00:00:00Z'),{latitude:0,longitude:179}),'light');
});
test('stored location rejects invalid or incomplete coordinates', () => {
 for(const value of [null,{}, {latitude:NaN,longitude:0},{latitude:91,longitude:0},{latitude:0,longitude:181},{latitude:'31',longitude:121}]) assert.equal(validLocation(value),false);
 assert.equal(validLocation({latitude:0,longitude:0}),true);
});
