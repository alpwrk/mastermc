document.getElementById('login').addEventListener('submit', async (e) => {
  e.preventDefault();
  const err = document.getElementById('error');
  err.hidden = true;
  const res = await fetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: document.getElementById('password').value }),
  });
  if (res.ok) {
    location.href = '/';
    return;
  }
  const data = await res.json().catch(() => ({}));
  err.textContent = data.error || 'Login failed';
  err.hidden = false;
});
