import assert from 'node:assert/strict';
import test from 'node:test';
import { localFileName } from '../files/fileNames';

test('adds a PDF extension to an extensionless Drive display title', () => {
  assert.equal(
    localFileName('TAM Report: Country Golf vs. Golf Country', 'application/pdf'),
    'TAM Report- Country Golf vs. Golf Country.pdf',
  );
});

test('preserves an existing type-bearing extension without changing its case', () => {
  assert.equal(localFileName('Quarterly Review.PDF', 'application/pdf'), 'Quarterly Review.PDF');
  assert.equal(
    localFileName('Board Review', 'application/vnd.openxmlformats-officedocument.presentationml.presentation'),
    'Board Review.pptx',
  );
  assert.equal(localFileName('Campaign.jpeg', 'image/jpeg'), 'Campaign.jpeg');
});

test('normalizes parameterized MIME types and keeps the extension inside the filename limit', () => {
  const result = localFileName('x'.repeat(200), 'application/pdf; charset=binary');
  assert.equal(result.length, 140);
  assert.match(result, /\.pdf$/);
});
