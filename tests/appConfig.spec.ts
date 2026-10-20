import { test, expect } from './fixtures';

test('offers to create the Timestral data source', async ({ appConfigPage, page }) => {
  await expect(
    page.getByRole('button', { name: /create data source and service account/i }).or(page.getByText('Data source ready'))
  ).toBeVisible();
});
