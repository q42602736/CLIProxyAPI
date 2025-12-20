module.exports = {
  apps: [{
    name: 'cliproxy',
    script: './cliproxy',
    args: '-config config.yaml',
    cwd: '/Users/caolin/Desktop/projects/CLIProxyAPI',
    interpreter: 'none',
    autorestart: true,
    watch: false,
    max_memory_restart: '1G',
    env: {
      NODE_ENV: 'production'
    },
    error_file: './logs/pm2-error.log',
    out_file: './logs/pm2-out.log',
    log_date_format: 'YYYY-MM-DD HH:mm:ss',
    merge_logs: true
  }]
};
