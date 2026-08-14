import json
import os
import re
from urllib.parse import quote
import pytest
from playwright.sync_api import expect, Page

def login_as_admin(page: Page, go_server: str, admin_password: str):
    page.on("pageerror", lambda err: print(f"JS EXCEPTION: {err}"))
    page.goto(go_server)
    # Click Guest indicator to open Sign In dialog
    page.click('#nav-login-btn')
    expect(page.locator('dialog#login-dialog')).to_be_visible()

    # The admin password is generated randomly on first startup, so it comes
    # from the admin_password fixture rather than a hardcoded default.
    page.fill('dialog#login-dialog input[name="username"]', 'admin')
    page.fill('dialog#login-dialog input[name="password"]', admin_password)
    page.click('dialog#login-dialog button[type="submit"]')
    
    # Wait for page reload/refresh
    page.wait_for_load_state('networkidle')
    expect(page.locator('#user-menu-trigger')).to_contain_text("admin")
    
    # Close the "Welcome back" toast to prevent it from blocking the toolbar
    expect(page.locator('.af-toast:has-text("Welcome back")')).to_be_visible()
    page.locator('.af-toast:has-text("Welcome back") button:has-text("×")').click()
    expect(page.locator('.af-toast:has-text("Welcome back")')).not_to_be_visible()

def test_stream_management(page, go_server, admin_password):
    # Log in
    login_as_admin(page, go_server, admin_password)
    
    # Go to Streams list page
    page.click('a[data-route="streams"]')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("Streams")
    
    # Open Create Stream Dialog
    page.click('button#create-stream-btn')
    expect(page.locator('dialog#create-stream-dialog')).to_be_visible()
    
    # Fill out Create Stream Form
    page.fill('dialog#create-stream-dialog input[name="stream_name"]', 'pytest-stream')
    page.check('dialog#create-stream-dialog input[name="retain_latest"]')
    page.fill('dialog#create-stream-dialog input[name="retain_latest_max_expiry"]', '45d')
    page.check('dialog#create-stream-dialog input[name="auto_expire_previous"]')
    
    # Submit Stream Creation
    page.click('dialog#create-stream-dialog button[type="submit"]')
    
    # Expect redirect to Stream details
    expect(page).to_have_url(f"{go_server}/_/streams/pytest-stream")
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("pytest-stream")
    
    # Open Stream Settings dialog
    page.click('#open-stream-settings-btn')
    expect(page.locator('dialog#manager-stream-settings-dialog')).to_be_visible()
    
    # Verify values are correctly set in the details form
    expect(page.locator('#detail-retain-latest')).to_be_checked()
    expect(page.locator('#detail-max-expiry')).to_have_value('45d')
    expect(page.locator('#detail-auto-expire-previous')).to_be_checked()
    
    # Edit values and save
    page.uncheck('#detail-retain-latest')
    page.fill('#detail-max-expiry', '60d')
    
    # Handle standard alert dialog
    page.once("dialog", lambda dialog: dialog.accept())
    page.click('#save-stream-settings-btn')
    
    # Refresh page and check if settings persisted
    page.reload()
    
    # Open Stream Settings dialog again
    page.click('#open-stream-settings-btn')
    expect(page.locator('dialog#manager-stream-settings-dialog')).to_be_visible()
    
    expect(page.locator('#detail-retain-latest')).not_to_be_checked()
    expect(page.locator('#detail-max-expiry')).to_have_value('60d')

def test_file_upload_with_stream_and_expiry(page, go_server, admin_password):
    # Log in
    login_as_admin(page, go_server, admin_password)
    
    # Click Upload to open Upload Dialog
    page.click('button#upload-btn')
    expect(page.locator('dialog#upload-dialog')).to_be_visible()
    
    # Fill Stream Name (should trigger debounce fetch)
    page.fill('#upload-stream-name', 'pytest-stream')
    
    # Wait for the inline configure button to reveal and pre-populate from details
    expect(page.locator('#upload-stream-configure-btn')).to_be_visible()
    expect(page.locator('#upload-stream-configure-btn')).to_have_text('⚙️ Update existing stream')
    
    # Click Configure Settings to open the popup with the values
    page.click('#upload-stream-configure-btn')
    expect(page.locator('dialog#upload-stream-settings-dialog')).to_be_visible()

    # Verify stream configuration pre-populated correctly from the previous test
    # (retain_latest = false, max_expiry = 60d, auto_expire_previous = true)
    expect(page.locator('#upload-stream-retain-latest')).not_to_be_checked()
    expect(page.locator('#upload-stream-retain-latest-max-expiry')).to_have_value('60d')
    expect(page.locator('#upload-stream-auto-expire-previous')).to_be_checked()

    # Update settings inside upload config
    page.check('#upload-stream-retain-latest')
    page.fill('#upload-stream-retain-latest-max-expiry', '30d')
    page.uncheck('#upload-stream-auto-expire-previous')

    # Save and close the stream settings
    page.click('#save-stream-settings-btn')
    
    # Fill Group Name
    page.fill('#upload-group-name', 'v1.0')
    
    # Configure Tags
    page.fill('dialog#upload-dialog input[name="tags"]', 'arch=x64,env=staging')
    
    # Expiration: Expire After Download (Sliding)
    page.select_option('#upload-expiry-type', 'after_download')
    expect(page.locator('#expiry-duration-wrapper')).to_be_visible()
    page.fill('#upload-expiry-duration', '15d')
    
    # Prepare mock file to upload
    temp_file = "test_upload_file.txt"
    with open(temp_file, "w") as f:
        f.write("browser test file content")
        
    try:
        # Set file to input
        page.set_input_files('input#file-input', temp_file)
        
        # Click upload button
        page.click('button#start-upload-btn')
        
        # Verify the file is uploaded and visible in the File Browser table
        expect(page.locator('.af-file-row:has-text("test_upload_file.txt") .af-file-text')).to_contain_text("test_upload_file.txt")
        
        # Verify stream/group badge is displayed
        expect(page.locator('.af-file-row:has-text("test_upload_file.txt") .badge-origin.badge-stream')).to_contain_text("pytest-stream/v1.0")
        
        # Verify tags are displayed (env=staging is an overflow tag, hidden until expanded)
        expect(page.locator('.af-file-row:has-text("test_upload_file.txt") .badge-tag:has-text("arch=x64")')).to_be_visible()
        expect(page.locator('.af-file-row:has-text("test_upload_file.txt") .af-extras-inner')).to_have_attribute("data-tags", re.compile(r"staging"))
        
        # Go to Streams, verify pytest-stream/v1.0 group lists the uploaded file
        page.click('a[data-route="streams"]')
        page.click('tr:has-text("pytest-stream")')
        expect(page.locator('.badge-origin.badge-group')).to_contain_text("pytest-stream/v1.0")
        # Stream view displays full path (not just basename)
        expect(page.locator('.af-file-row .af-file-text')).to_contain_text("/test_upload_file.txt")
        
    finally:
        if os.path.exists(temp_file):
            os.remove(temp_file)

def test_file_browsing_download_and_metadata(page, go_server, admin_password):
    login_as_admin(page, go_server, admin_password)
    
    # 1. Test directory creation in a normal (non-protected) area
    page.once("dialog", lambda dialog: dialog.accept("normal-dir"))
    page.click('button#new-directory-btn')
    
    # Wait for the folder to appear and enter it
    page.click('a.af-file-link:has-text("normal-dir")')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("normal-dir")
    
    # Go back to Root using breadcrumb
    page.click('a.af-breadcrumb-link:has-text("Root")')
    expect(page.locator('.af-breadcrumb-item')).to_have_count(1)
    
    # 2. Browse into the pre-created protected directory
    page.click('a.af-file-link:has-text("protected-dir")')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("protected-dir")
    
    # 3. Upload a file inside the protected directory
    page.click('button#upload-btn')
    expect(page.locator('dialog#upload-dialog')).to_be_visible()
    
    temp_file = "secure.txt"
    with open(temp_file, "w") as f:
        f.write("confidential data here")
        
    try:
        page.set_input_files('input#file-input', temp_file)
        page.click('button#start-upload-btn')
        
        # Verify file is listed
        expect(page.locator('.af-file-row:has-text("secure.txt") .af-file-text')).to_contain_text("secure.txt")
        
        # 4. Test serving/downloading the file (now triggers the inline preview modal)
        page.click('a.af-file-link:has-text("secure.txt")')
        expect(page.locator('.af-modal-preview')).to_be_visible()
        expect(page.locator('.af-modal-preview pre code')).to_contain_text("confidential data here")
        page.click('.af-modal-preview .modal-close')
        expect(page.locator('.af-modal-preview')).not_to_be_visible()
        
        # 5. Make the file Immutable via the Edit Meta dialog
        page.locator('.af-file-row:has-text("secure.txt") .af-menu-trigger').click()
        page.click('.edit-btn')
        expect(page.locator('dialog#edit-meta-dialog')).to_be_visible()
        
        page.check('dialog#edit-meta-dialog input[name="immutable"]')
        page.click('dialog#edit-meta-dialog button#save-btn')
        
        # Wait for the protection indicator to appear on the file icon
        expect(page.locator('.af-icon-protected')).to_be_visible()
        
        # 6. Open File Details dialog and verify protection badges & hashes
        page.locator('.af-file-row:has-text("secure.txt") .af-menu-trigger').click()
        page.click('.info-btn')
        expect(page.locator('dialog#file-info-dialog')).to_be_visible()
        
        # Check hashes are present (not 'N/A' or empty)
        expect(page.locator('#hash-sha256')).not_to_have_text('N/A')
        expect(page.locator('#hash-sha256')).not_to_have_text('')
        expect(page.locator('#hash-sha1')).not_to_have_text('N/A')
        expect(page.locator('#hash-md5')).not_to_have_text('N/A')
        
        # Check Access Policy badges are displayed
        expect(page.locator('.badge-policy-immutable')).to_contain_text("Immutable")
        expect(page.locator('.badge-policy-protected')).to_contain_text("System Protected")
        
        # Close details dialog
        page.click('dialog#file-info-dialog button:has-text("Close")')
        
    finally:
        if os.path.exists(temp_file):
            os.remove(temp_file)

def test_batch_download_merged(page, go_server, tmp_path, admin_password):
    import zipfile
    login_as_admin(page, go_server, admin_password)
    
    # 1. Create directory 'dirA'
    page.once("dialog", lambda dialog: dialog.accept("dirA"))
    page.click('button#new-directory-btn')
    page.click('a.af-file-link:has-text("dirA")')
    
    # Upload file1.txt in dirA
    page.click('button#upload-btn')
    temp_file1 = tmp_path / "file1.txt"
    temp_file1.write_text("content A")
    page.set_input_files('input#file-input', str(temp_file1))
    page.click('button#start-upload-btn')
    expect(page.locator('.af-file-row:has-text("file1.txt")')).to_be_visible()
    
    # Go back to Root
    page.goto(go_server)
    
    # 2. Create directory 'dirB'
    page.once("dialog", lambda dialog: dialog.accept("dirB"))
    page.click('button#new-directory-btn')
    page.click('a.af-file-link:has-text("dirB")')
    
    # Upload file2.txt in dirB
    page.click('button#upload-btn')
    temp_file2 = tmp_path / "file2.txt"
    temp_file2.write_text("content B")
    page.set_input_files('input#file-input', str(temp_file2))
    page.click('button#start-upload-btn')
    expect(page.locator('.af-file-row:has-text("file2.txt")')).to_be_visible()
    
    # Go back to Root
    page.goto(go_server)
    expect(page.locator('.af-file-row:has-text("dirA")')).to_be_visible()
    expect(page.locator('.af-file-row:has-text("dirB")')).to_be_visible()
    
    # Select dirA and dirB by hovering first
    row_dirA = page.locator('.af-file-row:has-text("dirA")')
    row_dirA.wait_for()
    row_dirA.hover()
    row_dirA.locator('input[type="checkbox"]').check()
    
    row_dirB = page.locator('.af-file-row:has-text("dirB")')
    row_dirB.wait_for()
    row_dirB.hover()
    row_dirB.locator('input[type="checkbox"]').check()
    
    # Batch actions should appear
    expect(page.locator('#batch-actions')).to_be_visible()
    
    # Click download dropdown
    page.click('#batch-download-dropdown-btn')
    
    # Click Merged Mode and wait for download
    with page.expect_download() as download_info:
        page.click('#batch-download-merged-btn')
        
    download = download_info.value
    download_path = tmp_path / "downloaded.zip"
    download.save_as(download_path)
    
    # Verify contents of the zip
    with zipfile.ZipFile(download_path, 'r') as zf:
        namelist = zf.namelist()
        assert "file1.txt" in namelist
        assert "file2.txt" in namelist
        
        assert zf.read("file1.txt").decode() == "content A"
        assert zf.read("file2.txt").decode() == "content B"

def test_user_management_and_upload(page, go_server, tmp_path, admin_password):
    login_as_admin(page, go_server, admin_password)
    
    # Open settings
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    expect(page.locator('.af-modal:has-text("Settings")')).to_be_visible()
    
    # Go to Users tab
    page.click('button[data-tab="users"]')
    
    # Click Add User
    page.click('#add-user-btn')
    expect(page.locator('.af-modal:has-text("Create User")')).to_be_visible()
    
    # Fill User Form
    page.fill('.af-modal:has-text("Create User") input[name="username"]', 'testuser1')
    page.fill('.af-modal:has-text("Create User") input[name="password"]', 'testpass')
    page.fill('.af-modal:has-text("Create User") input[name="allowed_paths"]', '/')
    page.click('.af-modal:has-text("Create User") button[type="submit"]')
    
    # Wait for settings refresh and verify user created
    expect(page.locator('#user-table-body td:has-text("testuser1")')).to_be_visible()
    
    # Try creating the same user again
    page.click('#add-user-btn')
    expect(page.locator('.af-modal:has-text("Create User")')).to_be_visible()
    
    page.fill('.af-modal:has-text("Create User") input[name="username"]', 'testuser1')
    page.fill('.af-modal:has-text("Create User") input[name="password"]', 'testpass')
    page.click('.af-modal:has-text("Create User") button[type="submit"]')
    
    # Verify the form remains open and error is shown
    expect(page.locator('.af-modal:has-text("Create User")')).to_be_visible()
    expect(page.locator('#form-error')).to_be_visible()
    expect(page.locator('#form-error')).to_contain_text("User with this username already exists")
    
    # Close the Create User modal
    page.click('.af-modal:has-text("Create User") #form-cancel')
    expect(page.locator('.af-modal:has-text("Create User")')).not_to_be_visible()
    
    # Close Settings Dialog
    page.click('dialog#settings-dialog .modal-close')
    
    # Logout
    page.click('#user-menu-trigger')
    page.click('#nav-logout-btn')
    
    # Wait for reload
    page.wait_for_load_state('networkidle')
    expect(page.locator('#nav-login-btn')).to_be_visible()
    
    # Login as new user
    page.click('#nav-login-btn')
    page.fill('dialog#login-dialog input[name="username"]', 'testuser1')
    page.fill('dialog#login-dialog input[name="password"]', 'testpass')
    page.click('dialog#login-dialog button[type="submit"]')
    
    page.wait_for_load_state('networkidle')
    expect(page.locator('#user-menu-trigger')).to_contain_text("testuser1")

    # Close the "Welcome back" toast to prevent it from blocking the toolbar
    expect(page.locator('.af-toast:has-text("Welcome back")')).to_be_visible()
    page.locator('.af-toast:has-text("Welcome back") button:has-text("×")').click()
    expect(page.locator('.af-toast:has-text("Welcome back")')).not_to_be_visible()

    # Upload a file
    page.click('button#upload-btn')
    
    temp_file = tmp_path / "user_test_file.txt"
    temp_file.write_text("user content")
    page.set_input_files('input#file-input', str(temp_file))
    page.click('button#start-upload-btn')
    
    # Verify the file is uploaded and visible in the File Browser table
    expect(page.locator('.af-file-row:has-text("user_test_file.txt") .af-file-text')).to_be_visible()


def test_token_management_and_upload(page, go_server, tmp_path, admin_password):
    login_as_admin(page, go_server, admin_password)
    
    # Open settings
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    expect(page.locator('.af-modal:has-text("Settings")')).to_be_visible()
    
    # Go to Tokens tab
    page.click('button[data-tab="tokens"]')
    
    # Click Generate New Token
    page.click('#gen-token-btn')
    expect(page.locator('.af-modal:has-text("Generate Automation Token")')).to_be_visible()
    
    # Fill Token Form
    page.fill('.af-modal:has-text("Generate Automation Token") input[name="name"]', 'Test Token')
    page.fill('.af-modal:has-text("Generate Automation Token") input[name="allowed_paths"]', '/')
    page.click('.af-modal:has-text("Generate Automation Token") button[type="submit"]')
    
    # Wait for the secret popup and get token
    expect(page.locator('#plain-token-val')).to_be_visible()
    token_secret = page.locator('#plain-token-val').inner_text()
    
    page.click('#secret-close')
    
    # Verify token in table
    expect(page.locator('#token-table-body td:has-text("Test Token")')).to_be_visible()
    
    # Close Settings Dialog
    page.click('dialog#settings-dialog .modal-close')
    
    # Upload file with API using the token
    test_content = b"content via token upload"
    response = page.request.put(
        f"{go_server}/token_file.txt",
        data=test_content,
        headers={
            "Authorization": f"Bearer {token_secret}"
        }
    )
    
    assert response.ok, f"Upload failed: {response.text()}"
    
    # Reload page and verify file is visible
    page.goto(go_server)
    expect(page.locator('.af-file-row:has-text("token_file.txt") .af-file-text')).to_be_visible()


def test_direct_url_navigation(page, go_server, tmp_path, admin_password):
    # Log in
    login_as_admin(page, go_server, admin_password)
    
    # 1. Direct navigation to the root directory
    page.goto(f"{go_server}/")
    expect(page.locator('.af-file-row:has-text("protected-dir")')).to_be_visible()
    
    # 2. Direct navigation to a sub-directory
    page.goto(f"{go_server}/protected-dir")
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("protected-dir")
    
    # Upload a file in protected-dir to test direct file download
    page.click('button#upload-btn')
    temp_file = tmp_path / "direct_download.txt"
    temp_file.write_text("direct text")
    page.set_input_files('input#file-input', str(temp_file))
    page.click('button#start-upload-btn')
    expect(page.locator('.af-file-row:has-text("direct_download.txt")')).to_be_visible()
    
    # 3. Direct navigation to the file
    # Text files are rendered by the browser directly.
    response = page.goto(f"{go_server}/protected-dir/direct_download.txt")
    assert response.status == 200
    # Playwright's response.text() gets the body of the response
    assert response.text() == "direct text"

    # 4. Direct navigation to a non-existent path
    page.goto(f"{go_server}/does-not-exist-dir")
    expect(page.locator("h2:has-text('Resource Not Found')")).to_be_visible()

def test_file_browser_click_behaviour(page, go_server, tmp_path, admin_password):
    login_as_admin(page, go_server, admin_password)

    # Create a subdirectory to navigate into
    page.once("dialog", lambda dialog: dialog.accept("click-test-dir"))
    page.click('button#new-directory-btn')
    expect(page.locator('.af-file-row:has-text("click-test-dir")')).to_be_visible()

    # Enter the directory by clicking the link text (normal behaviour)
    page.click('a.af-file-link:has-text("click-test-dir")')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("click-test-dir")

    # The ".." back row should be visible
    expect(page.locator('.af-back-row')).to_be_visible()

    # Click the ".." text span to go back
    page.locator('.af-back-row .af-file-text').click()
    expect(page.locator('.af-breadcrumb-item')).to_have_count(1)
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("Root")

    # Navigate back in and verify clicking the link element itself also works
    page.click('a.af-file-link:has-text("click-test-dir")')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("click-test-dir")

    page.locator('.af-back-row a.af-file-link').click()
    expect(page.locator('.af-breadcrumb-item')).to_have_count(1)
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("Root")

    # Verify clicking the directory link text navigates into it
    page.locator('.af-file-row:has-text("click-test-dir") .af-file-text').click()
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("click-test-dir")


def test_stream_with_null_files(page, go_server, admin_password):
    login_as_admin(page, go_server, admin_password)
    
    # Mock the API response for getStreamGroups to return a group with null files
    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "name": "mock-stream",
                "groups": [
                    {
                        "name": "empty-group",
                        "files": None
                    }
                ]
            }
        )
    
    page.route("**/_/api/v1/streams/mock-stream", handle_route)
    
    # Navigate directly to the mock stream details page
    page.goto(f"{go_server}/_/streams/mock-stream")
    
    # Expect the page to load successfully and display the group header
    expect(page.locator('.badge-group')).to_contain_text("mock-stream/empty-group")
    expect(page.locator('.af-group-badge')).to_contain_text("0 items")

def test_stream_group_view_renders_files(page, go_server, admin_password):
    login_as_admin(page, go_server, admin_password)
    
    # Mock the API response for getStreamGroups to return 2 groups with 2 files each (one of which is a directory)
    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "name": "mock-stream",
                "groups": [
                    {
                        "name": "group-a",
                        "files": [
                            {"name": "file1.txt", "path": "/mock-stream/group-a/file1.txt", "size": 100, "isdir": False, "type": "file", "modTime": "2026-06-01T20:00:00Z", "policy": {"is_allowed": True}},
                            {"name": "file2.txt", "path": "/mock-stream/group-a/file2.txt", "size": 200, "isdir": False, "type": "file", "modTime": "2026-06-01T20:01:00Z", "policy": {"is_allowed": True}}
                        ]
                    },
                    {
                        "name": "group-b",
                        "files": [
                            {"name": "dir1", "path": "/mock-stream/group-b/dir1", "size": 0, "isdir": True, "type": "dir", "modTime": "2026-06-01T20:02:00Z", "policy": {"is_allowed": True}},
                            {"name": "file4.txt", "path": "/mock-stream/group-b/file4.txt", "size": 400, "isdir": False, "type": "file", "modTime": "2026-06-01T20:03:00Z", "policy": {"is_allowed": True}}
                        ]
                    }
                ]
            }
        )
        
    page.route("**/_/api/v1/streams/mock-stream", handle_route)
    
    # Navigate directly to the mock stream details page
    page.goto(f"{go_server}/_/streams/mock-stream")
    
    # Expect the group headers to be visible
    expect(page.locator('.badge-group:has-text("mock-stream/group-a")')).to_be_visible()
    expect(page.locator('.badge-group:has-text("mock-stream/group-b")')).to_be_visible()
    
    # Expect the items count badge to show "2 items" for both groups
    expect(page.locator('.af-group-row:has-text("group-a") .af-group-badge')).to_contain_text("2 items")
    expect(page.locator('.af-group-row:has-text("group-b") .af-group-badge')).to_contain_text("2 items")
    
    # Expect all 4 resource names to be visible
    expect(page.locator('.af-file-text:has-text("mock-stream/group-a/file1.txt")')).to_be_visible()
    expect(page.locator('.af-file-text:has-text("mock-stream/group-a/file2.txt")')).to_be_visible()
    expect(page.locator('.af-file-text:has-text("mock-stream/group-b/dir1")')).to_be_visible()
    expect(page.locator('.af-file-text:has-text("mock-stream/group-b/file4.txt")')).to_be_visible()

    # Verify that the directory mock-stream/group-b/dir1 has the folder icon and "-" as size
    expect(page.locator('tr.af-file-row:has-text("mock-stream/group-b/dir1") .af-file-icon')).to_contain_text('📁')
    expect(page.locator('tr.af-file-row:has-text("mock-stream/group-b/dir1") td.af-col-mono').first).to_contain_text('-')

    # Verify that the file mock-stream/group-a/file1.txt has the file icon and correct size
    expect(page.locator('tr.af-file-row:has-text("mock-stream/group-a/file1.txt") .af-file-icon')).to_contain_text('📄')
    expect(page.locator('tr.af-file-row:has-text("mock-stream/group-a/file1.txt") td.af-col-mono').first).to_contain_text('100 Bytes')

def test_stream_file_link_uses_full_path(page, go_server, admin_password):
    """Download link in stream view must use file.path (full path), not file.name (basename)."""
    login_as_admin(page, go_server, admin_password)

    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "name": "mock-stream",
                "groups": [
                    {
                        "name": "g1",
                        "files": [
                            {
                                "name": "nested.txt",
                                "path": "/subdir/nested.txt",
                                "size": 42,
                                "isdir": False,
                                "type": "file",
                                "modTime": "2026-06-01T12:00:00Z",
                                "policy": {"is_allowed": True}
                            }
                        ]
                    }
                ]
            }
        )

    page.route("**/_/api/v1/streams/mock-stream", handle_route)
    page.goto(f"{go_server}/_/streams/mock-stream")

    # The file row must be visible with the full path as display text
    file_row = page.locator('tr.af-file-row:has-text("/subdir/nested.txt")')
    expect(file_row).to_be_visible()

    # The display text must be the full path
    expect(file_row.locator('.af-file-text')).to_have_text("/subdir/nested.txt")

    # The link href must point to the full path, not just the basename
    link = file_row.locator('a.af-file-link')
    href = link.get_attribute("href")
    assert href == "/subdir/nested.txt", f"Expected href '/subdir/nested.txt', got '{href}'"


def test_stream_name_with_slash_roundtrips(page, go_server, admin_password):
    """A stream name containing '/' must be URL-encoded into the route and
    decoded back when loading the detail view (full end-to-end via backend)."""
    login_as_admin(page, go_server, admin_password)

    stream_name = "team/alpha"

    # Create the stream via the UI.
    page.click('a[data-route="streams"]')
    page.click('button#create-stream-btn')
    expect(page.locator('dialog#create-stream-dialog')).to_be_visible()
    page.fill('dialog#create-stream-dialog input[name="stream_name"]', stream_name)
    page.click('dialog#create-stream-dialog button[type="submit"]')

    # The slash must be percent-encoded in the URL, not split into a new segment.
    expect(page).to_have_url(f"{go_server}/_/streams/team%2Falpha")

    # And the detail view must show the decoded name in the breadcrumb.
    expect(page.locator('.af-breadcrumb-current')).to_have_text(stream_name)

    # Returning to the list and clicking the row must navigate back correctly.
    page.click('a.af-breadcrumb-link')
    expect(page.locator('.af-breadcrumb-current')).to_contain_text("Streams")
    row = page.locator(f'.af-file-row:has-text("{stream_name}")')
    expect(row).to_be_visible()
    row.click()
    expect(page).to_have_url(f"{go_server}/_/streams/team%2Falpha")
    expect(page.locator('.af-breadcrumb-current')).to_have_text(stream_name)


def test_stream_group_view_with_slash_in_names(page, go_server, admin_password):
    """Stream/group names containing '/' must render in the group badge and the
    detail view must load from a percent-encoded URL."""
    login_as_admin(page, go_server, admin_password)

    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "name": "team/alpha",
                "groups": [
                    {
                        "name": "build/42",
                        "files": [
                            {"name": "f.txt", "path": "/p/f.txt", "size": 1, "isdir": False, "type": "file", "modTime": "2026-06-01T12:00:00Z", "policy": {"is_allowed": True}}
                        ]
                    }
                ]
            }
        )

    # The frontend must request the encoded path.
    page.route("**/_/api/v1/streams/team%2Falpha", handle_route)
    page.goto(f"{go_server}/_/streams/team%2Falpha")

    # Breadcrumb and group badge must show the decoded names verbatim.
    expect(page.locator('.af-breadcrumb-current')).to_have_text("team/alpha")
    expect(page.locator('.badge-group')).to_have_text("team/alpha/build/42")


def test_stream_view_escapes_html_in_names(page, go_server, admin_password):
    """User-controlled stream/group/path values must be HTML-escaped, not
    injected as live markup (XSS hardening)."""
    login_as_admin(page, go_server, admin_password)

    payload = '<img src=x onerror="window.__xss=1">'

    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "name": payload,
                "groups": [
                    {
                        "name": payload,
                        "files": [
                            {"name": "n.txt", "path": payload, "size": 1, "isdir": False, "type": "file", "modTime": "2026-06-01T12:00:00Z", "policy": {"is_allowed": True}}
                        ]
                    }
                ]
            }
        )

    # The route name in the URL is irrelevant here (the response is mocked);
    # use a harmless slug and let the mock supply the malicious values.
    page.route("**/_/api/v1/streams/*", handle_route)
    page.goto(f"{go_server}/_/streams/xss-probe")

    # group.name and file.path (from the response) must render as text, escaped.
    expect(page.locator('.badge-group')).to_be_visible()
    expect(page.locator('.af-file-text')).to_have_text(payload)

    # The injected <img> must NOT exist anywhere as live markup, and the
    # onerror handler must never have fired.
    assert page.locator('.badge-group img').count() == 0
    assert page.locator('.af-file-text img').count() == 0
    assert page.evaluate("() => window.__xss") is None


def test_audit_log_access_and_display(page, go_server, admin_password):
    """Admin can open the Audit Log dialog and see table entries."""
    login_as_admin(page, go_server, admin_password)

    # Upload a file first so the audit log has at least one entry
    page.click('button#upload-btn')
    import tempfile, os
    with tempfile.NamedTemporaryFile(suffix=".txt", delete=False, mode="w") as tf:
        tf.write("audit log ui test")
        tmp_path = tf.name
    try:
        page.set_input_files('input#file-input', tmp_path)
        page.click('button#start-upload-btn')
        expect(page.locator('.af-file-row:has-text("' + os.path.basename(tmp_path) + '")')).to_be_visible()
    finally:
        os.unlink(tmp_path)

    # Open Settings
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    expect(page.locator('dialog#settings-dialog')).to_be_visible()

    # Audit Log tab should be visible for admin
    audit_tab = page.locator('button[data-tab="audit"]')
    expect(audit_tab).to_be_visible()

    # Click it — opens the audit log dialog (not tab content)
    audit_tab.click()
    expect(page.locator('dialog#af-audit-dialog')).to_be_visible()

    # Table is rendered with at least one entry row
    expect(page.locator('#af-audit-tbody .af-audit-entry-row').first).to_be_visible()

    # Every visible row has a time cell, an action badge, and a status badge
    first_row = page.locator('#af-audit-tbody .af-audit-entry-row').first
    expect(first_row.locator('.af-audit-action')).to_be_visible()
    expect(first_row.locator('.badge-success, .badge-danger')).to_be_visible()

    # Entry count indicator is shown
    expect(page.locator('#af-audit-count')).not_to_have_text('')


def test_audit_log_row_expand(page, go_server, admin_password):
    """Clicking an audit log row expands extra fields."""
    login_as_admin(page, go_server, admin_password)

    # Open audit log dialog directly via settings
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    page.locator('button[data-tab="audit"]').click()
    expect(page.locator('dialog#af-audit-dialog')).to_be_visible()
    expect(page.locator('#af-audit-tbody .af-audit-entry-row').first).to_be_visible()

    # Extras row should be hidden before click
    extras_row = page.locator('#af-audit-tbody .af-audit-extras-row').first
    expect(extras_row).not_to_be_visible()

    # Click first entry row to expand
    page.locator('#af-audit-tbody .af-audit-entry-row').first.click()
    expect(extras_row).to_be_visible()
    expect(page.locator('.af-audit-extras').first).to_be_visible()

    # Click again to collapse
    page.locator('#af-audit-tbody .af-audit-entry-row').first.click()
    expect(extras_row).not_to_be_visible()


def test_audit_log_filter(page, go_server, admin_password):
    """Typing in the filter box reduces visible entries."""
    login_as_admin(page, go_server, admin_password)

    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    page.locator('button[data-tab="audit"]').click()
    expect(page.locator('dialog#af-audit-dialog')).to_be_visible()
    expect(page.locator('#af-audit-tbody .af-audit-entry-row').first).to_be_visible()

    unfiltered_count = page.locator('#af-audit-tbody .af-audit-entry-row').count()

    # Filter to something that will match a subset (FILE_UPLOAD is a known action)
    page.fill('#af-audit-filter', 'FILE_UPLOAD')
    page.wait_for_timeout(600)  # debounce is 400ms

    filtered_count = page.locator('#af-audit-tbody .af-audit-entry-row').count()
    assert filtered_count <= unfiltered_count, (
        f"Filter should reduce or keep entry count, got {filtered_count} vs {unfiltered_count}"
    )

    # Every visible action badge should say FILE_UPLOAD
    for badge in page.locator('#af-audit-tbody .af-audit-action').all():
        assert badge.inner_text() == 'FILE_UPLOAD', f"Unexpected action: {badge.inner_text()}"

    # Clear filter restores entries
    page.fill('#af-audit-filter', '')
    page.wait_for_timeout(600)
    restored_count = page.locator('#af-audit-tbody .af-audit-entry-row').count()
    assert restored_count >= filtered_count


def test_audit_log_non_admin_has_no_tab(page, go_server, admin_password):
    """Non-admin users do not see the Audit Log tab in Settings."""
    login_as_admin(page, go_server, admin_password)

    # Create a regular user
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    page.click('button[data-tab="users"]')
    page.click('#add-user-btn')
    expect(page.locator('.af-modal:has-text("Create User")')).to_be_visible()
    page.fill('.af-modal:has-text("Create User") input[name="username"]', 'noaudit-user')
    page.fill('.af-modal:has-text("Create User") input[name="password"]', 'noaudit123')
    page.fill('.af-modal:has-text("Create User") input[name="allowed_paths"]', '/')
    page.click('.af-modal:has-text("Create User") button[type="submit"]')
    expect(page.locator('#user-table-body td:has-text("noaudit-user")')).to_be_visible()
    page.click('dialog#settings-dialog .modal-close')

    # Logout and log back in as the regular user
    page.click('#user-menu-trigger')
    page.click('#nav-logout-btn')
    page.wait_for_load_state('networkidle')
    page.click('#nav-login-btn')
    page.fill('dialog#login-dialog input[name="username"]', 'noaudit-user')
    page.fill('dialog#login-dialog input[name="password"]', 'noaudit123')
    page.click('dialog#login-dialog button[type="submit"]')
    page.wait_for_load_state('networkidle')

    # Open Settings
    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    expect(page.locator('dialog#settings-dialog')).to_be_visible()

    # Audit Log tab must not be visible for non-admin
    expect(page.locator('button[data-tab="audit"]')).not_to_be_visible()


def test_audit_log_escapes_html_in_entries(page, go_server, admin_password):
    """Audit entries record attacker-supplied data verbatim (the resource is the
    uploaded path), and the log is rendered in an *admin's* browser. A payload
    planted by anyone who can write one path must render as text, never as live
    markup, or writing a file becomes privilege escalation."""
    login_as_admin(page, go_server, admin_password)

    payload = '<img src=x onerror="window.__xss=1">'

    def handle_route(route):
        route.fulfill(
            status=200,
            content_type="application/json",
            json={
                "entries": [
                    {
                        "time": "2026-06-01T12:00:00Z",
                        "action": "FILE_UPLOAD",
                        "resource": payload,
                        "status": "SUCCESS",
                        "user": payload,
                        "token_name": payload,
                        "ip": payload,
                        "reason": payload,
                        "extra_field": payload,
                    }
                ],
                "next_before_offset": -1,
                "next_generation": 0,
                "has_more": False,
                "scan_limit_hit": False,
            },
        )

    page.route("**/_/api/v1/admin/audit-log*", handle_route)

    page.click('#user-menu-trigger')
    page.click('#nav-settings-btn')
    page.click('button[data-tab="audit"]')
    expect(page.locator('#af-audit-tbody tr').first).to_be_visible()

    # Expand the row so the extras (also user-controlled) are rendered too.
    page.click('#af-audit-tbody tr.af-audit-entry-row')

    assert page.locator('#af-audit-tbody img').count() == 0
    assert page.evaluate("() => window.__xss") is None
    expect(page.locator('.af-audit-resource').first).to_have_text(payload)


def test_toast_escapes_html_in_message(page, go_server, admin_password):
    """Toasts mostly carry server error strings, which echo back paths and
    filenames chosen by whoever uploaded them."""
    login_as_admin(page, go_server, admin_password)

    fired = page.evaluate("""async () => {
        const { showToast } = await import('/_/static/js/components/Toast.js');
        showToast('Action prohibited: /<img src=x onerror="window.__xss=1">.txt', 'error');
        // The batch-delete path passes per-item detail lines, which are paths.
        showToast('Failed to delete 1 item:', 'error', null, 0,
                  ['/<img src=x onerror="window.__xss=1">.txt — immutable']);
        await new Promise(r => setTimeout(r, 300));
        const c = document.querySelector('.af-toast-container');
        return { imgs: c.querySelectorAll('img').length, text: c.innerText };
    }""")

    assert fired["imgs"] == 0
    assert page.evaluate("() => window.__xss") is None
    # The payload is still shown to the user, just as inert text.
    assert "<img" in fired["text"]


@pytest.mark.parametrize("filename", ["hash#name.txt", "query?name.txt", "100%done.txt"])
def test_upload_preserves_special_characters_in_filename(
    page, go_server, admin_password, filename
):
    """The filename becomes a URL segment, so it must be percent-encoded.
    Unencoded, a '#' started a fragment and a '?' a query: uploading
    'a#b.txt' returned 200 OK and silently stored a file called 'a'."""
    login_as_admin(page, go_server, admin_password)

    page.evaluate("""async (name) => {
        const { TransferManager } = await import('/_/static/js/api/TransferManager.js');
        const f = new File(["content-of-" + name], name, { type: "text/plain" });
        TransferManager.upload(f, '/', '/', {});
        await new Promise(r => setTimeout(r, 800));
    }""", filename)

    # The file must exist under its full name, with its content intact. The
    # filename is quoted here for the same reason the upload path encodes it:
    # unescaped, "#" and "?" would not survive into the request URL.
    resp = page.request.get(f"{go_server}/{quote(filename)}")
    assert resp.status == 200, f"{filename} was not stored under its full name"
    assert resp.text() == f"content-of-{filename}"


def test_row_actions_reachable_without_hover(page, go_server, admin_password):
    """The row action menu is hover-revealed. `opacity: 0` alone left the
    trigger focusable while invisible, so a keyboard user tabbed onto a control
    they could not see, and a touch device — which never hovers — could not
    reveal it at all."""
    login_as_admin(page, go_server, admin_password)

    import tempfile
    with tempfile.NamedTemporaryFile(suffix=".txt", delete=False, mode="w") as tf:
        tf.write("row actions a11y")
        tmp_path = tf.name
    try:
        page.click('button#upload-btn')
        page.set_input_files('input#file-input', tmp_path)
        page.click('button#start-upload-btn')
        expect(page.locator('.af-file-row:has-text("' + os.path.basename(tmp_path) + '")')).to_be_visible()
    finally:
        os.unlink(tmp_path)

    trigger_is_focused = """() => document.activeElement.classList.contains('af-menu-trigger')
                              && !!document.activeElement.closest('tr.af-file-row')"""

    # Hidden at rest, so the table stays quiet.
    assert page.evaluate(
        "() => getComputedStyle(document.querySelector('tr.af-file-row .af-row-actions')).opacity"
    ) == "0"

    # Reachable by keyboard, and *visible* once focused.
    for _ in range(60):
        page.keyboard.press("Tab")
        if page.evaluate(trigger_is_focused):
            break
    else:
        pytest.fail("row action trigger is not reachable with the Tab key")

    # Wait past the 0.2s opacity transition before sampling.
    page.wait_for_timeout(400)
    assert page.evaluate(
        "() => getComputedStyle(document.activeElement.closest('.af-row-actions')).opacity"
    ) == "1", "menu is focused but still invisible"

    # Enter opens it and focus moves into the menu.
    page.keyboard.press("Enter")
    page.wait_for_timeout(200)
    assert page.evaluate(
        """() => !document.activeElement.closest('.af-row-actions-container')
                   .querySelector('.af-row-dropdown').classList.contains('hidden')"""
    ), "Enter did not open the row menu"


def test_row_actions_visible_on_touch_devices(browser, go_server, admin_password):
    """A touch device never fires :hover, so the hover-reveal alone would leave
    the menu permanently invisible on a phone."""
    ctx = browser.new_context(
        has_touch=True, is_mobile=True, viewport={"width": 390, "height": 844},
        user_agent="Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15",
    )
    page = ctx.new_page()
    try:
        # The session is established through the API rather than the login
        # dialog: at a 390px viewport the navbar collapses and the Guest button
        # is not hittable, which has nothing to do with what this test covers.
        token = ctx.request.post(
            f"{go_server}/_/api/login",
            data={"username": "admin", "password": admin_password},
        ).json()["token"]
        ctx.request.put(
            f"{go_server}/touch-probe.txt",
            headers={"Authorization": f"Bearer {token}"},
            data="touch probe",
        )

        page.goto(go_server)
        page.evaluate(
            """(t) => {
                localStorage.setItem('af_token', t);
                localStorage.setItem('af_user', JSON.stringify({username:'admin', isAdmin:true}));
            }""",
            token,
        )
        page.goto(go_server)

        assert page.evaluate("() => matchMedia('(hover: none)').matches"), \
            "expected a non-hover device for this test"
        expect(page.locator('tr.af-file-row').first).to_be_visible()
        assert page.evaluate(
            "() => getComputedStyle(document.querySelector('tr.af-file-row .af-row-actions')).opacity"
        ) == "1", "row actions are invisible on a device that cannot hover"
    finally:
        ctx.close()


@pytest.mark.parametrize("status,payload", [
    (403, 'Action prohibited: /<img src=x onerror="window.__xss=1"> is immutable'),
    (500, 'boom <img src=x onerror="window.__xss=1">'),
])
def test_router_error_views_escape_server_messages(page, go_server, status, payload):
    """The router renders the server's error string into the page. Those strings
    embed the requested path ("Action prohibited: /<path> is immutable"), which
    is chosen by whoever uploaded the file, so they must render as text."""
    page.route(
        "**/_/api/v1/fs/**",
        lambda route: route.fulfill(
            status=status,
            content_type="application/json",
            body=json.dumps({"error": payload}),
        ),
    )
    page.goto(f"{go_server}/some-path/")
    expect(page.locator('#app')).to_be_visible()

    assert page.locator('#app img').count() == 0, "server error string was parsed as markup"
    assert page.evaluate("() => window.__xss") is None
