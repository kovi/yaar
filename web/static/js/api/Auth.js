
export const Auth = {
    getToken: () => localStorage.getItem('af_token'),
    getUser: () => JSON.parse(localStorage.getItem('af_user') || 'null'),
    
    saveSession(data) {
        localStorage.setItem('af_token', data.token);
        localStorage.setItem('af_user', JSON.stringify({
            username: data.username,
            isAdmin: data.is_admin
        }));
    },
    
    logout() {
        localStorage.removeItem('af_token');
        localStorage.removeItem('af_user');
        window.location.reload(); // Hard reset is safest for auth
    },

    isLoggedIn() {
        return !!this.getToken();
    }
};