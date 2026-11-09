using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Linq;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Markup;
using System.Windows.Media;
using System.Windows.Threading;

namespace FleetWarden.Tray
{
    // The self-service window: the software catalog, "install updates now"
    // and help requests. Menu on the left, like the web interface. It asks
    // the agent for the catalog when it opens and every few seconds while it
    // is visible; every action answers with the new catalog too.
    public sealed class SelfServiceWindow : Window
    {
        const string Layout = @"
<Grid xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'
      xmlns:x='http://schemas.microsoft.com/winfx/2006/xaml'
      Background='{DynamicResource Surface}'
      TextElement.FontFamily='Segoe UI Variable Text, Segoe UI' TextElement.Foreground='{DynamicResource Fg}' TextElement.FontSize='13'>
  <Grid.Resources>
    <Style x:Key='Btn' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Height' Value='34'/>
      <Setter Property='Padding' Value='14,0'/>
      <Setter Property='FontSize' Value='13'/>
      <Setter Property='FontWeight' Value='SemiBold'/>
      <Setter Property='BorderThickness' Value='1'/>
      <Setter Property='Background' Value='{DynamicResource Accent}'/>
      <Setter Property='BorderBrush' Value='{DynamicResource Accent}'/>
      <Setter Property='Foreground' Value='{DynamicResource AccentFg}'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' BorderBrush='{TemplateBinding BorderBrush}'
                    BorderThickness='{TemplateBinding BorderThickness}' Padding='{TemplateBinding Padding}'>
              <ContentPresenter HorizontalAlignment='Center' VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.88'/></Trigger>
              <Trigger Property='IsPressed' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.75'/></Trigger>
              <Trigger Property='IsEnabled' Value='False'><Setter TargetName='Bd' Property='Opacity' Value='0.5'/></Trigger>
              <Trigger Property='IsKeyboardFocused' Value='True'><Setter TargetName='Bd' Property='BorderBrush' Value='{DynamicResource Fg}'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
    <Style x:Key='Secondary' TargetType='Button' BasedOn='{StaticResource Btn}'>
      <Setter Property='Background' Value='{DynamicResource Surface2}'/>
      <Setter Property='BorderBrush' Value='{DynamicResource Line}'/>
      <Setter Property='Foreground' Value='{DynamicResource Fg}'/>
    </Style>
    <Style x:Key='Nav' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Height' Value='38'/>
      <Setter Property='Margin' Value='0,0,0,4'/>
      <Setter Property='Padding' Value='12,0'/>
      <Setter Property='FontSize' Value='13'/>
      <Setter Property='HorizontalContentAlignment' Value='Stretch'/>
      <Setter Property='Background' Value='Transparent'/>
      <Setter Property='Foreground' Value='{DynamicResource SidebarFg}'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' Padding='{TemplateBinding Padding}'>
              <ContentPresenter VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.85'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
    <Style x:Key='Box' TargetType='TextBox'>
      <Setter Property='FontSize' Value='13'/>
      <Setter Property='Foreground' Value='{DynamicResource Fg}'/>
      <Setter Property='CaretBrush' Value='{DynamicResource Fg}'/>
      <Setter Property='Background' Value='{DynamicResource Surface}'/>
      <Setter Property='BorderBrush' Value='{DynamicResource Line}'/>
      <Setter Property='Padding' Value='8,7'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='TextBox'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' BorderBrush='{TemplateBinding BorderBrush}' BorderThickness='1'>
              <ScrollViewer x:Name='PART_ContentHost' Margin='{TemplateBinding Padding}' VerticalScrollBarVisibility='Auto'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsKeyboardFocused' Value='True'><Setter TargetName='Bd' Property='BorderBrush' Value='{DynamicResource Accent}'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
  </Grid.Resources>
  <Grid.ColumnDefinitions>
    <ColumnDefinition Width='232'/>
    <ColumnDefinition Width='*'/>
  </Grid.ColumnDefinitions>
  <Border Background='{DynamicResource Sidebar}'>
    <DockPanel Margin='16,20,16,16' LastChildFill='False'>
      <StackPanel DockPanel.Dock='Top' Orientation='Horizontal' Margin='4,0,0,24'>
        <Viewbox Width='30' Height='30'>
          <Canvas Width='64' Height='64'>
            <Path Fill='{DynamicResource Accent}' Data='M32,4 L8,13 L8,30 C8,45.5 18.2,56.6 32,60 C45.8,56.6 56,45.5 56,30 L56,13 Z'/>
            <Path Fill='{DynamicResource Inner}' Data='M32,10.5 L14,17.3 L14,30 C14,42 21.6,50.9 32,53.9 C42.4,50.9 50,42 50,30 L50,17.3 Z'/>
            <Path Stroke='{DynamicResource Dots}' StrokeThickness='2.2' StrokeStartLineCap='Round' StrokeEndLineCap='Round'
                  Data='M32,28.5 L32,33.5 M29,27 L24,34.5 M35,27 L40,34.5'/>
            <Ellipse Canvas.Left='27.5' Canvas.Top='19.5' Width='9' Height='9' Fill='#FFFFFF'/>
            <Ellipse Canvas.Left='18' Canvas.Top='34' Width='8' Height='8' Fill='{DynamicResource Dots}'/>
            <Ellipse Canvas.Left='38' Canvas.Top='34' Width='8' Height='8' Fill='{DynamicResource Dots}'/>
          </Canvas>
        </Viewbox>
        <StackPanel Margin='10,0,0,0' VerticalAlignment='Center'>
          <TextBlock x:Name='Product' FontSize='15' FontWeight='SemiBold' Foreground='#FFFFFF'/>
          <TextBlock x:Name='SelfService' FontSize='12' Foreground='{DynamicResource SidebarFg}'/>
        </StackPanel>
      </StackPanel>
      <StackPanel x:Name='Nav' DockPanel.Dock='Top'/>
      <TextBlock x:Name='ManagedBy' DockPanel.Dock='Bottom' Margin='4,0,0,0' FontSize='11' TextWrapping='Wrap' Foreground='{DynamicResource SidebarFg}'/>
    </DockPanel>
  </Border>
  <Grid Grid.Column='1'>
    <Grid.RowDefinitions>
      <RowDefinition Height='Auto'/>
      <RowDefinition Height='*'/>
      <RowDefinition Height='Auto'/>
    </Grid.RowDefinitions>
    <StackPanel Margin='32,26,32,16'>
      <TextBlock x:Name='Heading' FontSize='22' FontWeight='SemiBold'/>
      <TextBlock x:Name='Subheading' Margin='0,6,0,0' TextWrapping='Wrap' LineHeight='19' Foreground='{DynamicResource Muted}'/>
    </StackPanel>
    <ScrollViewer x:Name='Scroll' Grid.Row='1' VerticalScrollBarVisibility='Auto' Padding='32,0,32,24'>
      <ContentControl x:Name='Page' Focusable='False'/>
    </ScrollViewer>
    <Border x:Name='Status' Grid.Row='2' Visibility='Collapsed' Margin='32,0,32,20' CornerRadius='8' Padding='14,10' BorderThickness='1'>
      <TextBlock x:Name='StatusText' TextWrapping='Wrap'/>
    </Border>
  </Grid>
</Grid>";

        public enum Tab { Software, Updates, Help }

        readonly Func<TrayMessage, Task<SelfServiceReply>> request;
        readonly FrameworkElement root;
        readonly StackPanel nav;
        readonly ContentControl page;
        readonly TextBlock heading, subheading, statusText, product;
        readonly Border status;
        readonly DispatcherTimer poll;
        readonly Dictionary<Tab, Button> navButtons = new Dictionary<Tab, Button>();
        // Persistent controls, so typing survives a refresh.
        readonly TextBox search, subject, message, contact;
        readonly StackPanel softwareList = new StackPanel(), helpList = new StackPanel(), updatesBody = new StackPanel();
        readonly WrapPanel chips = new WrapPanel { Margin = new Thickness(0, 12, 0, 16) };
        readonly StackPanel softwarePage, helpPage;
        SelfServiceCatalog catalog;
        Tab tab;
        string category;
        long expanded; // the approval item whose reason box is open
        TextBox reasonBox;
        ContentControl helpNotice;
        Border helpForm;
        TextBlock helpHeader;
        bool busy, loaded;

        public SelfServiceWindow(Func<TrayMessage, Task<SelfServiceReply>> onRequest, string accent, Tab start = Tab.Software)
        {
            request = onRequest;
            tab = start;
            Width = 900;
            Height = 640;
            MinWidth = 720;
            MinHeight = 480;
            WindowStartupLocation = WindowStartupLocation.CenterScreen;
            Theme.Apply(Resources, accent);
            root = (FrameworkElement)XamlReader.Parse(Layout);
            Content = root;
            nav = (StackPanel)root.FindName("Nav");
            page = (ContentControl)root.FindName("Page");
            heading = T("Heading");
            subheading = T("Subheading");
            statusText = T("StatusText");
            product = T("Product");
            status = (Border)root.FindName("Status");
            T("SelfService").Text = SsText.SelfService;
            T("ManagedBy").Text = SsText.ManagedBy;
            SetProduct("FleetWarden");

            AddNav(Tab.Software, SsText.NavSoftware, "M4,4 h7 v7 h-7 z M13,4 h7 v7 h-7 z M4,13 h7 v7 h-7 z M13,13 h7 v7 h-7 z");
            AddNav(Tab.Updates, SsText.NavUpdates, "M12,4 a8,8 0 1 1 -7.4,5 M4,4 v5 h5");
            AddNav(Tab.Help, SsText.NavHelp, "M12,3 a9,9 0 1 0 0.01,0 z M9.5,9.5 a2.5,2.5 0 1 1 3.5,2.3 c-0.6,0.3 -1,0.8 -1,1.5 v0.7 M12,17 v0.5");

            search = Box(SsText.Search);
            search.TextChanged += (s, e) => RenderSoftware();
            softwarePage = new StackPanel();
            softwarePage.Children.Add(Watermarked(search, SsText.Search));
            softwarePage.Children.Add(chips);
            softwarePage.Children.Add(softwareList);

            subject = Box(SsText.SubjectHint);
            message = Box("");
            message.AcceptsReturn = true;
            message.TextWrapping = TextWrapping.Wrap;
            message.MinHeight = 110;
            message.VerticalContentAlignment = VerticalAlignment.Top;
            contact = Box("");
            helpPage = new StackPanel();
            BuildHelpForm();

            poll = new DispatcherTimer { Interval = TimeSpan.FromSeconds(10) };
            poll.Tick += async (s, e) => { if (IsVisible && expanded == 0) await Send(new TrayMessage { Op = "catalog" }, quiet: true); };
            Loaded += async (s, e) =>
            {
                poll.Start();
                await Send(new TrayMessage { Op = "catalog" }, quiet: true);
            };
            IsVisibleChanged += (s, e) => { if (IsVisible) poll.Start(); else poll.Stop(); };
            ShowTab(tab);
        }

        public void Activate(Tab t)
        {
            ShowTab(t);
            if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
            Activate();
            _ = Send(new TrayMessage { Op = "catalog" }, quiet: true);
        }

        TextBlock T(string name) => (TextBlock)root.FindName(name);

        void SetProduct(string name)
        {
            product.Text = name;
            Title = SsText.WindowTitle(name);
        }

        void AddNav(Tab t, string label, string icon)
        {
            var g = new Grid();
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            g.ColumnDefinitions.Add(new ColumnDefinition());
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var path = new System.Windows.Shapes.Path
            {
                Data = Geometry.Parse(icon), Width = 18, Height = 18, Stretch = Stretch.Uniform, StrokeThickness = 1.8,
                StrokeStartLineCap = PenLineCap.Round, StrokeEndLineCap = PenLineCap.Round, StrokeLineJoin = PenLineJoin.Round,
            };
            path.SetBinding(System.Windows.Shapes.Shape.StrokeProperty, new System.Windows.Data.Binding("Foreground") { RelativeSource = new System.Windows.Data.RelativeSource(System.Windows.Data.RelativeSourceMode.FindAncestor, typeof(Button), 1) });
            g.Children.Add(path);
            var text = new TextBlock { Text = label, Margin = new Thickness(10, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center };
            Grid.SetColumn(text, 1);
            g.Children.Add(text);
            var badge = new Border { CornerRadius = new CornerRadius(9), Padding = new Thickness(7, 1, 7, 1), Visibility = Visibility.Collapsed };
            badge.SetResourceReference(Border.BackgroundProperty, "Accent");
            var bt = new TextBlock { FontSize = 11, FontWeight = FontWeights.SemiBold };
            bt.SetResourceReference(TextBlock.ForegroundProperty, "AccentFg");
            badge.Child = bt;
            Grid.SetColumn(badge, 2);
            g.Children.Add(badge);
            var b = new Button { Content = g, Style = (Style)root.FindResource("Nav"), Tag = badge };
            b.Click += (s, e) => ShowTab(t);
            navButtons[t] = b;
            nav.Children.Add(b);
        }

        void SetBadge(Tab t, int n)
        {
            var badge = (Border)navButtons[t].Tag;
            badge.Visibility = n > 0 ? Visibility.Visible : Visibility.Collapsed;
            ((TextBlock)badge.Child).Text = n.ToString();
        }

        void ShowTab(Tab t)
        {
            tab = t;
            foreach (var kv in navButtons)
            {
                var sel = kv.Key == t;
                if (sel)
                {
                    kv.Value.SetResourceReference(BackgroundProperty, "SidebarActive");
                    kv.Value.Foreground = Brushes.White;
                }
                else
                {
                    kv.Value.Background = Brushes.Transparent;
                    kv.Value.SetResourceReference(ForegroundProperty, "SidebarFg");
                }
                kv.Value.FontWeight = sel ? FontWeights.SemiBold : FontWeights.Normal;
            }
            switch (t)
            {
                case Tab.Software:
                    heading.Text = SsText.SoftwareTitle;
                    subheading.Text = SsText.SoftwareSubtitle;
                    page.Content = softwarePage;
                    RenderSoftware();
                    break;
                case Tab.Updates:
                    heading.Text = SsText.UpdatesTitle;
                    subheading.Text = SsText.UpdatesSubtitle;
                    page.Content = updatesBody;
                    RenderUpdates();
                    break;
                case Tab.Help:
                    heading.Text = SsText.HelpTitle;
                    subheading.Text = SsText.HelpSubtitle;
                    page.Content = helpPage;
                    RenderHelp();
                    break;
            }
        }

        // ---- talking to the agent ----

        // Send asks the agent and shows the outcome; it reports success.
        public async Task<bool> Send(TrayMessage m, bool quiet = false)
        {
            if (busy && !quiet) return false;
            if (!quiet) busy = true;
            m.Type = "ss";
            SelfServiceReply r;
            try { r = await request(m); }
            catch (Exception) { r = new SelfServiceReply { Error = "offline" }; }
            finally { if (!quiet) busy = false; }
            if (r == null) r = new SelfServiceReply { Error = "offline" };
            if (r.Catalog != null) Apply(r.Catalog);
            else if (!loaded) Render();
            if (!string.IsNullOrEmpty(r.Error) && (!quiet || catalog == null)) ShowStatus(string.IsNullOrEmpty(r.Message) ? SsText.Error(r.Error) : r.Message, error: true);
            else if (!quiet && !string.IsNullOrEmpty(r.Message)) ShowStatus(r.Message, error: false);
            return string.IsNullOrEmpty(r.Error);
        }

        public void Apply(SelfServiceCatalog c)
        {
            catalog = c;
            loaded = true;
            if (!string.IsNullOrWhiteSpace(c.ProductName)) SetProduct(c.ProductName.Trim());
            Theme.Apply(Resources, c.AccentColor);
            if (status.Visibility == Visibility.Visible && statusIsError && statusText.Text == SsText.Error("offline")) status.Visibility = Visibility.Collapsed;
            Render();
        }

        bool statusIsError;

        void ShowStatus(string text, bool error)
        {
            statusIsError = error;
            statusText.Text = text;
            status.SetResourceReference(Border.BackgroundProperty, "Surface2");
            status.SetResourceReference(Border.BorderBrushProperty, error ? "Danger" : "Accent");
            status.Visibility = Visibility.Visible;
        }

        void Render()
        {
            SetBadge(Tab.Updates, catalog != null && catalog.Updates ? catalog.PendingUpdates : 0);
            SetBadge(Tab.Help, catalog?.HelpRequests?.Count(h => h.State != "closed" && !string.IsNullOrEmpty(h.Reply)) ?? 0);
            ShowTab(tab);
        }

        // ---- software ----

        void RenderSoftware()
        {
            softwareList.Children.Clear();
            chips.Children.Clear();
            if (catalog == null)
            {
                softwareList.Children.Add(Muted(SsText.Loading));
                return;
            }
            if (!catalog.Software)
            {
                softwareList.Children.Add(Notice(SsText.SoftwareOff));
                return;
            }
            var items = catalog.Items ?? new SelfServiceItem[0];
            if (items.Length == 0)
            {
                softwareList.Children.Add(Notice(SsText.NoItems));
                return;
            }
            var cats = items.Select(i => Cat(i)).Distinct().OrderBy(c => c == SsText.Other ? "￿" : c, StringComparer.CurrentCultureIgnoreCase).ToList();
            if (cats.Count > 1)
            {
                chips.Children.Add(Chip(SsText.AllCategories, null));
                foreach (var c in cats) chips.Children.Add(Chip(c, c));
            }
            var q = (search.Text ?? "").Trim();
            var shown = items.Where(i => (category == null || Cat(i) == category) &&
                (q == "" || Contains(i.Name, q) || Contains(i.Publisher, q) || Contains(i.Description, q) || Contains(i.Category, q))).ToList();
            if (shown.Count == 0)
            {
                softwareList.Children.Add(Muted(SsText.NoMatch));
                return;
            }
            foreach (var i in shown) softwareList.Children.Add(ItemCard(i));
        }

        static string Cat(SelfServiceItem i) => string.IsNullOrWhiteSpace(i.Category) ? SsText.Other : i.Category.Trim();

        static bool Contains(string v, string q) => v != null && v.IndexOf(q, StringComparison.CurrentCultureIgnoreCase) >= 0;

        Button Chip(string label, string value)
        {
            var sel = category == value;
            var b = new Button { Content = label, Style = (Style)root.FindResource(sel ? "Btn" : "Secondary"), Height = 30, Margin = new Thickness(0, 0, 8, 8),
                FontWeight = FontWeights.Normal, Padding = new Thickness(12, 0, 12, 0) };
            b.Click += (s, e) => { category = value; RenderSoftware(); };
            return b;
        }

        FrameworkElement ItemCard(SelfServiceItem i)
        {
            var card = Card();
            var g = new Grid();
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            g.ColumnDefinitions.Add(new ColumnDefinition());
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var tile = new Border { Width = 40, Height = 40, CornerRadius = new CornerRadius(10), VerticalAlignment = VerticalAlignment.Top };
            tile.SetResourceReference(Border.BackgroundProperty, "Accent");
            var letter = new TextBlock { Text = (i.Name ?? "?").Trim().Substring(0, Math.Min(1, (i.Name ?? "?").Trim().Length)).ToUpperInvariant(),
                FontSize = 18, FontWeight = FontWeights.SemiBold, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
            letter.SetResourceReference(TextBlock.ForegroundProperty, "AccentFg");
            tile.Child = letter;
            g.Children.Add(tile);

            var text = new StackPanel { Margin = new Thickness(14, 0, 16, 0) };
            text.Children.Add(new TextBlock { Text = i.Name, FontSize = 14, FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.Wrap });
            var meta = string.Join(" · ", new[] { i.Publisher, i.Version, i.Approval ? SsText.NeedsApproval : null }.Where(x => !string.IsNullOrWhiteSpace(x)));
            if (meta != "") text.Children.Add(Muted(meta, 12, new Thickness(0, 2, 0, 0)));
            if (!string.IsNullOrWhiteSpace(i.Description)) text.Children.Add(new TextBlock { Text = i.Description.Trim(), Margin = new Thickness(0, 6, 0, 0), TextWrapping = TextWrapping.Wrap, LineHeight = 19 });
            if (i.State == SelfServiceItem.Failed) text.Children.Add(Tone(SsText.Failed(i.Message), "Danger"));
            if (i.State == SelfServiceItem.Rejected) text.Children.Add(Tone(SsText.Rejected(i.Message), "Danger"));
            if (expanded == i.Id)
            {
                text.Children.Add(Muted(SsText.ReasonLabel, 12, new Thickness(0, 12, 0, 6)));
                if (reasonBox == null)
                {
                    reasonBox = Box("");
                    reasonBox.AcceptsReturn = true;
                    reasonBox.TextWrapping = TextWrapping.Wrap;
                    reasonBox.MinHeight = 60;
                    reasonBox.MaxLength = 500;
                }
                if (reasonBox.Parent is Panel old) old.Children.Remove(reasonBox);
                text.Children.Add(reasonBox);
                var row = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 10, 0, 0) };
                var sendBtn = Btn(SsText.SendRequest, "Btn");
                sendBtn.Click += async (s, e) =>
                {
                    var reason = reasonBox.Text;
                    expanded = 0;
                    reasonBox = null;
                    await Send(new TrayMessage { Op = "install", ItemId = i.Id, Reason = reason });
                };
                var cancel = Btn(SsText.Cancel, "Secondary");
                cancel.Margin = new Thickness(8, 0, 0, 0);
                cancel.Click += (s, e) => { expanded = 0; reasonBox = null; RenderSoftware(); };
                row.Children.Add(sendBtn);
                row.Children.Add(cancel);
                text.Children.Add(row);
                Dispatcher.BeginInvoke(DispatcherPriority.Input, new Action(() => reasonBox?.Focus()));
            }
            Grid.SetColumn(text, 1);
            g.Children.Add(text);

            FrameworkElement action;
            switch (i.State)
            {
                case SelfServiceItem.Pending: action = Pill(SsText.Pending, "Warning"); break;
                case SelfServiceItem.Queued: action = Pill(SsText.Queued, "Accent"); break;
                case SelfServiceItem.Installing: action = Pill(SsText.Installing, "Accent"); break;
                case SelfServiceItem.Installed: action = Pill(SsText.Installed, "Success"); break;
                default:
                    if (expanded == i.Id) { action = new Border(); break; }
                    var label = i.State == SelfServiceItem.Failed ? SsText.TryAgain : i.State == SelfServiceItem.Rejected ? SsText.AskAgain
                        : i.Approval ? SsText.Request : SsText.Install;
                    var b = Btn(label, i.State == SelfServiceItem.Available ? "Btn" : "Secondary");
                    b.Click += async (s, e) =>
                    {
                        if (i.Approval) { expanded = i.Id; reasonBox = null; RenderSoftware(); return; }
                        await Send(new TrayMessage { Op = "install", ItemId = i.Id });
                    };
                    action = b;
                    break;
            }
            action.VerticalAlignment = VerticalAlignment.Top;
            Grid.SetColumn(action, 2);
            g.Children.Add(action);
            card.Child = g;
            return card;
        }

        // ---- updates ----

        void RenderUpdates()
        {
            updatesBody.Children.Clear();
            if (catalog == null)
            {
                updatesBody.Children.Add(Muted(SsText.Loading));
                return;
            }
            if (!catalog.Updates)
            {
                updatesBody.Children.Add(Notice(SsText.UpdatesOff));
                return;
            }
            var card = Card();
            var sp = new StackPanel();
            var running = SsText.UpdateRunning(catalog.UpdateState);
            var n = catalog.PendingUpdates;
            sp.Children.Add(new TextBlock { Text = n > 0 ? SsText.UpdatesReady(n) : SsText.UpToDate, FontSize = 18, FontWeight = FontWeights.SemiBold });
            var when = SsText.When(catalog.LastScan);
            if (when != "") sp.Children.Add(Muted(SsText.LastScan(when), 12, new Thickness(0, 4, 0, 0)));
            if (catalog.RebootPending) sp.Children.Add(Tone(SsText.RestartPending, "Warning"));
            var st = SsText.UpdateState(catalog.UpdateState);
            if (st != "") sp.Children.Add(Tone(st, catalog.UpdateState == "failed" ? "Danger" : catalog.UpdateState == "completed" ? "Success" : "Accent"));
            if (n > 0 || catalog.RebootPending)
            {
                sp.Children.Add(Muted(SsText.UpdatesHint, 12, new Thickness(0, 14, 0, 0)));
                var b = Btn(n > 0 ? SsText.InstallUpdates : SsText.RestartNow, "Btn");
                b.HorizontalAlignment = HorizontalAlignment.Left;
                b.Margin = new Thickness(0, 14, 0, 0);
                b.IsEnabled = !running;
                b.Click += async (s, e) => await Send(new TrayMessage { Op = "updates" });
                sp.Children.Add(b);
            }
            card.Child = sp;
            updatesBody.Children.Add(card);
        }

        // ---- help ----

        // BuildHelpForm makes the form once, so a refresh keeps what the user
        // types and where the cursor is.
        void BuildHelpForm()
        {
            helpNotice = new ContentControl { Focusable = false };
            helpForm = Card();
            var sp = new StackPanel();
            sp.Children.Add(Label(SsText.Subject, 0));
            sp.Children.Add(Watermarked(subject, SsText.SubjectHint));
            sp.Children.Add(Label(SsText.Message, 12));
            sp.Children.Add(message);
            sp.Children.Add(Label(SsText.Contact, 12));
            sp.Children.Add(contact);
            var send = Btn(SsText.Send, "Btn");
            send.HorizontalAlignment = HorizontalAlignment.Left;
            send.Margin = new Thickness(0, 16, 0, 0);
            send.Click += async (s, e) =>
            {
                if (string.IsNullOrWhiteSpace(subject.Text))
                {
                    ShowStatus(SsText.SubjectRequired, error: true);
                    subject.Focus();
                    return;
                }
                var help = new HelpRequest { Subject = subject.Text.Trim(), Message = message.Text, Contact = contact.Text.Trim() };
                var ok = await Send(new TrayMessage { Op = "help", Help = help });
                if (ok)
                {
                    subject.Text = "";
                    message.Text = "";
                }
            };
            sp.Children.Add(send);
            helpForm.Child = sp;
            helpHeader = new TextBlock { Text = SsText.YourRequests, FontSize = 15, FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 12, 0, 10) };
            helpPage.Children.Add(helpNotice);
            helpPage.Children.Add(helpForm);
            helpPage.Children.Add(helpHeader);
            helpPage.Children.Add(helpList);
        }

        void RenderHelp()
        {
            helpList.Children.Clear();
            helpNotice.Content = null;
            var on = catalog != null && catalog.Help;
            helpForm.Visibility = helpHeader.Visibility = on ? Visibility.Visible : Visibility.Collapsed;
            if (catalog == null)
            {
                helpNotice.Content = Muted(SsText.Loading);
                return;
            }
            if (!on)
            {
                helpNotice.Content = Notice(SsText.HelpOff);
                return;
            }
            if (!string.IsNullOrWhiteSpace(catalog.HelpText)) helpNotice.Content = Notice(catalog.HelpText.Trim());
            var list = catalog.HelpRequests ?? new HelpTicket[0];
            if (list.Length == 0) helpList.Children.Add(Muted(SsText.NoRequests));
            foreach (var h in list)
            {
                var c = Card();
                var cs = new StackPanel();
                var top = new DockPanel();
                var pill = Pill(SsText.TicketState(h.State), h.State == "closed" ? "Muted" : h.State == "in_progress" ? "Accent" : "Warning");
                DockPanel.SetDock(pill, Dock.Right);
                top.Children.Add(pill);
                top.Children.Add(new TextBlock { Text = $"#{h.Id}  {h.Subject}", FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center });
                cs.Children.Add(top);
                cs.Children.Add(Muted(SsText.When(h.CreatedAt), 12, new Thickness(0, 2, 0, 0)));
                if (!string.IsNullOrWhiteSpace(h.Reply))
                {
                    var quote = new Border { BorderThickness = new Thickness(3, 0, 0, 0), Padding = new Thickness(10, 4, 0, 4), Margin = new Thickness(0, 10, 0, 0) };
                    quote.SetResourceReference(Border.BorderBrushProperty, "Accent");
                    var qs = new StackPanel();
                    qs.Children.Add(Muted(SsText.ReplyFromIT, 12, new Thickness(0)));
                    qs.Children.Add(new TextBlock { Text = h.Reply.Trim(), TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 2, 0, 0), LineHeight = 19 });
                    quote.Child = qs;
                    cs.Children.Add(quote);
                }
                c.Child = cs;
                helpList.Children.Add(c);
            }
        }

        // ---- building blocks ----

        Border Card()
        {
            var b = new Border { CornerRadius = new CornerRadius(12), BorderThickness = new Thickness(1), Padding = new Thickness(18), Margin = new Thickness(0, 0, 0, 12) };
            b.SetResourceReference(Border.BackgroundProperty, "Surface");
            b.SetResourceReference(Border.BorderBrushProperty, "Line");
            return b;
        }

        Border Notice(string text)
        {
            var b = Card();
            b.SetResourceReference(Border.BackgroundProperty, "Surface2");
            b.Child = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, LineHeight = 19 };
            return b;
        }

        Button Btn(string label, string style) => new Button { Content = label, Style = (Style)root.FindResource(style) };

        TextBox Box(string tooltip)
        {
            var t = new TextBox { Style = (Style)root.FindResource("Box") };
            if (!string.IsNullOrEmpty(tooltip)) AutomationProperties(t, tooltip);
            return t;
        }

        static void AutomationProperties(TextBox t, string name) => System.Windows.Automation.AutomationProperties.SetName(t, name);

        // Watermarked shows a hint inside an empty text box.
        static FrameworkElement Watermarked(TextBox box, string hint)
        {
            var g = new Grid();
            var w = new TextBlock { Text = hint, Margin = new Thickness(11, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center, IsHitTestVisible = false };
            w.SetResourceReference(TextBlock.ForegroundProperty, "Muted");
            void Update() => w.Visibility = string.IsNullOrEmpty(box.Text) ? Visibility.Visible : Visibility.Collapsed;
            box.TextChanged += (s, e) => Update();
            Update();
            g.Children.Add(box);
            g.Children.Add(w);
            return g;
        }

        static TextBlock Label(string text, double top) => new TextBlock { Text = text, FontWeight = FontWeights.SemiBold, FontSize = 12, Margin = new Thickness(0, top, 0, 6) };

        static TextBlock Muted(string text, double size = 13, Thickness? margin = null)
        {
            var t = new TextBlock { Text = text, FontSize = size, TextWrapping = TextWrapping.Wrap, Margin = margin ?? new Thickness(0, 4, 0, 0) };
            t.SetResourceReference(TextBlock.ForegroundProperty, "Muted");
            return t;
        }

        static TextBlock Tone(string text, string brush)
        {
            var t = new TextBlock { Text = text, FontSize = 12, FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 8, 0, 0) };
            t.SetResourceReference(TextBlock.ForegroundProperty, brush);
            return t;
        }

        static Border Pill(string text, string brush)
        {
            var b = new Border { CornerRadius = new CornerRadius(12), Padding = new Thickness(10, 4, 10, 4), BorderThickness = new Thickness(1), Height = 26 };
            b.SetResourceReference(Border.BorderBrushProperty, brush);
            var t = new TextBlock { Text = text, FontSize = 12, FontWeight = FontWeights.SemiBold, VerticalAlignment = VerticalAlignment.Center };
            t.SetResourceReference(TextBlock.ForegroundProperty, brush);
            b.Child = t;
            return b;
        }

        protected override void OnClosing(CancelEventArgs e)
        {
            poll.Stop();
            base.OnClosing(e);
        }
    }
}
